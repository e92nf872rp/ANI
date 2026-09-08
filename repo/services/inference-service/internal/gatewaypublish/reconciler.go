package gatewaypublish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
)

// Reconciler performs at most one database-claimed projection per RunOnce.
type Reconciler struct {
	store             repository.PublicationStore
	kube              KubeAPI
	publicBase        string
	owner             string
	lease             time.Duration
	requestTimeout    time.Duration
	statusTimeout     time.Duration
	gatewayController string
	now               func() time.Time
}

type publicationLease struct {
	target    repository.PublicationTarget
	nextRenew time.Time
}

var errPublicationLeaseLost = errors.New("publication lease lost")

func NewReconciler(store repository.PublicationStore, kube KubeAPI, cfg Config, owner string, now func() time.Time) *Reconciler {
	if now == nil {
		now = time.Now
	}
	return &Reconciler{store: store, kube: kube, publicBase: strings.TrimRight(cfg.PublicBaseURL.String(), "/"), owner: owner, lease: cfg.LeaseDuration, requestTimeout: cfg.RequestTimeout, statusTimeout: cfg.StatusTimeout, gatewayController: cfg.GatewayController, now: now}
}

func (r *Reconciler) RunOnce(ctx context.Context) (bool, error) {
	target, claimed, err := r.store.ClaimPublication(ctx, r.owner, r.now(), r.lease)
	if err != nil || !claimed {
		return claimed, stableError("GATEWAY_CLAIM_FAILED", err)
	}
	lease := &publicationLease{target: target}
	if err := r.renewLease(ctx, lease, true); err != nil {
		return true, stableError("GATEWAY_LEASE_RENEW_FAILED", err)
	}
	switch target.Desired {
	case domain.PublicationPublished:
		return true, r.publish(ctx, target, lease)
	case domain.PublicationUnpublished:
		return true, r.unpublish(ctx, target, lease)
	default:
		return true, r.fail(ctx, target, lease, "GATEWAY_DESIRED_INVALID")
	}
}

func (r *Reconciler) publish(ctx context.Context, target repository.PublicationTarget, lease *publicationLease) error {
	objects, err := Render(target)
	if err != nil {
		return r.fail(ctx, target, lease, "GATEWAY_TARGET_INVALID")
	}
	for _, object := range []Object{objects.Backend, objects.AIServiceBackend, objects.AIGatewayRoute} {
		if err := r.apply(ctx, lease, object); err != nil {
			if errors.Is(err, errPublicationLeaseLost) {
				return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
			}
			return r.fail(ctx, target, lease, applyReason(object.Kind, err))
		}
	}
	if reason := r.ready(ctx, lease, objects); reason != "" {
		if reason == "GATEWAY_LEASE_RENEW_FAILED" {
			return errors.New(reason)
		}
		return r.fail(ctx, target, lease, reason)
	}
	path, ok := service.OpenAIPathForTask(target.Task)
	if !ok {
		return r.fail(ctx, target, lease, "GATEWAY_TASK_UNSUPPORTED")
	}
	return r.complete(ctx, lease, repository.PublicationResult{TenantID: target.TenantID, ServiceID: target.ServiceID, Generation: target.Generation, LeaseToken: target.LeaseToken, Phase: domain.PublicationPublishedOK, InvocationURL: r.publicBase + path, Now: r.now()})
}

func (r *Reconciler) unpublish(ctx context.Context, target repository.PublicationTarget, lease *publicationLease) error {
	name := ResourceName(target.ServiceID)
	if err := r.delete(ctx, lease, KindAIGatewayRoute, gatewayNamespace, name); err != nil {
		if errors.Is(err, errPublicationLeaseLost) {
			return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
		}
		return r.fail(ctx, target, lease, deleteReason(err))
	}
	if err := r.waitNotFound(ctx, lease, KindAIGatewayRoute, gatewayNamespace, name); err != nil {
		if errors.Is(err, errPublicationLeaseLost) {
			return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
		}
		return r.fail(ctx, target, lease, unpublishWaitReason(err))
	}
	if err := r.delete(ctx, lease, KindAIServiceBackend, gatewayNamespace, name); err != nil {
		if errors.Is(err, errPublicationLeaseLost) {
			return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
		}
		return r.fail(ctx, target, lease, deleteReason(err))
	}
	if err := r.waitNotFound(ctx, lease, KindAIServiceBackend, gatewayNamespace, name); err != nil {
		if errors.Is(err, errPublicationLeaseLost) {
			return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
		}
		return r.fail(ctx, target, lease, unpublishWaitReason(err))
	}
	if err := r.delete(ctx, lease, KindBackend, gatewayNamespace, name); err != nil {
		if errors.Is(err, errPublicationLeaseLost) {
			return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
		}
		return r.fail(ctx, target, lease, deleteReason(err))
	}
	if err := r.waitNotFound(ctx, lease, KindBackend, gatewayNamespace, name); err != nil {
		if errors.Is(err, errPublicationLeaseLost) {
			return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
		}
		return r.fail(ctx, target, lease, unpublishWaitReason(err))
	}
	return r.complete(ctx, lease, repository.PublicationResult{TenantID: target.TenantID, ServiceID: target.ServiceID, Generation: target.Generation, LeaseToken: target.LeaseToken, Phase: domain.PublicationUnpublishedOK, Now: r.now()})
}

func (r *Reconciler) ready(ctx context.Context, lease *publicationLease, rendered Objects) string {
	checks := []struct {
		kind     Kind
		name     string
		accepted string
	}{
		{KindGateway, gatewayName, "Programmed"},
		{KindBackend, rendered.Backend.Name, "Accepted"},
		{KindAIServiceBackend, rendered.AIServiceBackend.Name, "Accepted"},
	}
	for _, check := range checks {
		if reason := r.waitCurrent(ctx, lease, check.kind, check.name, func(object Object) (bool, string) {
			conditions, ok := conditionsFromStatus(object.Status)
			return ok && currentTrue(conditions, check.accepted, object.Generation), ""
		}); reason != "" {
			return reason
		}
	}
	return r.waitCurrent(ctx, lease, KindAIGatewayRoute, rendered.AIGatewayRoute.Name, func(route Object) (bool, string) {
		if _, ok := route.Status["parents"]; !ok {
			return false, "GATEWAY_ROUTE_STATUS_UNSUPPORTED"
		}
		return r.routeParentReady(route.Status, route.Generation), ""
	})
}

func (r *Reconciler) waitCurrent(ctx context.Context, lease *publicationLease, kind Kind, name string, ready func(Object) (bool, string)) string {
	deadline, cancel := context.WithTimeout(ctx, r.statusTimeout)
	defer cancel()
	ticker := time.NewTicker(r.pollInterval())
	defer ticker.Stop()
	sawStale := false
	for {
		if err := r.renewLease(deadline, lease, false); err != nil {
			return "GATEWAY_LEASE_RENEW_FAILED"
		}
		object, err := r.get(deadline, kind, gatewayNamespace, name)
		if err == nil {
			if ok, immediate := ready(object); immediate != "" {
				return immediate
			} else if ok {
				return ""
			}
			sawStale = true
		} else if !errors.Is(err, ErrNotFound) {
			return statusReason(kind, err)
		}
		select {
		case <-deadline.Done():
			if sawStale {
				return statusStaleReason(kind)
			}
			return "GATEWAY_STATUS_TIMEOUT"
		case <-ticker.C:
		}
	}
}

func (r *Reconciler) apply(ctx context.Context, lease *publicationLease, object Object) error {
	if err := r.renewLease(ctx, lease, false); err != nil {
		return err
	}
	request, cancel := r.requestContext(ctx)
	defer cancel()
	_, err := r.kube.Apply(request, object)
	return err
}

func (r *Reconciler) get(ctx context.Context, kind Kind, namespace, name string) (Object, error) {
	request, cancel := r.requestContext(ctx)
	defer cancel()
	return r.kube.Get(request, kind, namespace, name)
}

func (r *Reconciler) delete(ctx context.Context, lease *publicationLease, kind Kind, namespace, name string) error {
	if err := r.renewLease(ctx, lease, false); err != nil {
		return err
	}
	request, cancel := r.requestContext(ctx)
	defer cancel()
	return r.kube.Delete(request, managedReference(lease.target, kind, namespace, name))
}

func managedReference(target repository.PublicationTarget, kind Kind, namespace, name string) Object {
	return Object{
		Kind: kind, Namespace: namespace, Name: name, Generation: target.Generation,
		Body: map[string]any{"metadata": map[string]any{
			"name": name, "namespace": namespace, "labels": ownerLabels(target),
		}},
	}
}

func (r *Reconciler) waitNotFound(ctx context.Context, lease *publicationLease, kind Kind, namespace, name string) error {
	deadline, cancel := context.WithTimeout(ctx, r.statusTimeout)
	defer cancel()
	ticker := time.NewTicker(r.pollInterval())
	defer ticker.Stop()
	for {
		if err := r.renewLease(deadline, lease, false); err != nil {
			return err
		}
		_, err := r.get(deadline, kind, namespace, name)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(deadline.Err(), context.DeadlineExceeded)) {
			return context.DeadlineExceeded
		}
		if err != nil {
			return err
		}
		if deadline.Err() != nil {
			return deadline.Err()
		}
		select {
		case <-deadline.Done():
			return deadline.Err()
		case <-ticker.C:
		}
	}
}

func (r *Reconciler) complete(ctx context.Context, lease *publicationLease, result repository.PublicationResult) error {
	if err := r.renewLease(ctx, lease, true); err != nil {
		return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
	}
	result.Now = r.now()
	request, cancel := r.requestContext(ctx)
	defer cancel()
	if err := r.store.CompletePublication(request, result); err != nil {
		return stableError("GATEWAY_COMPLETE_FAILED", err)
	}
	return nil
}

func (r *Reconciler) fail(ctx context.Context, target repository.PublicationTarget, lease *publicationLease, reason string) error {
	if err := r.renewLease(ctx, lease, true); err != nil {
		return stableError("GATEWAY_LEASE_RENEW_FAILED", err)
	}
	request, cancel := r.requestContext(ctx)
	defer cancel()
	if err := r.store.FailPublication(request, target, reason, r.now()); err != nil {
		return stableError("GATEWAY_FAILURE_RECORD_FAILED", err)
	}
	return errors.New(reason)
}

func (r *Reconciler) renewLease(ctx context.Context, lease *publicationLease, force bool) error {
	now := r.now().UTC()
	if !force && !lease.nextRenew.IsZero() && now.Before(lease.nextRenew) {
		return nil
	}
	request, cancel := r.requestContext(ctx)
	defer cancel()
	if err := r.store.RenewPublication(request, lease.target, now, r.lease); err != nil {
		return fmt.Errorf("%w", errPublicationLeaseLost)
	}
	lease.nextRenew = now.Add(r.lease / 2)
	return nil
}

func (r *Reconciler) pollInterval() time.Duration {
	interval := 200 * time.Millisecond
	if leaseInterval := r.lease / 3; leaseInterval > 0 && leaseInterval < interval {
		interval = leaseInterval
	}
	if interval <= 0 {
		return time.Millisecond
	}
	return interval
}

func (r *Reconciler) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, r.requestTimeout)
}

func currentTrue(conditions []Condition, typ string, generation int64) bool {
	matches := 0
	for _, condition := range conditions {
		if condition.Type == typ && condition.Status == "True" && condition.ObservedGeneration == generation {
			matches++
		}
	}
	return matches == 1
}

func conditionsFromStatus(status map[string]any) ([]Condition, bool) {
	values, ok := status["conditions"].([]any)
	if !ok {
		return nil, false
	}
	conditions := make([]Condition, 0, len(values))
	for _, value := range values {
		condition, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		typ, typeOK := condition["type"].(string)
		state, stateOK := condition["status"].(string)
		generation, generationOK := int64Value(condition["observedGeneration"])
		if !typeOK || !stateOK || !generationOK {
			return nil, false
		}
		conditions = append(conditions, Condition{Type: typ, Status: state, ObservedGeneration: generation})
	}
	return conditions, true
}

func (r *Reconciler) routeParentReady(status map[string]any, generation int64) bool {
	parents, ok := status["parents"].([]any)
	if !ok {
		return false
	}
	for _, value := range parents {
		parent, ok := value.(map[string]any)
		if !ok || parent["controllerName"] != r.gatewayController {
			continue
		}
		ref, ok := parent["parentRef"].(map[string]any)
		if !ok || ref["group"] != "gateway.networking.k8s.io" || ref["kind"] != "Gateway" || ref["name"] != gatewayName {
			continue
		}
		if namespace, exists := ref["namespace"]; exists && namespace != gatewayNamespace {
			continue
		}
		conditions, ok := conditionsFromValue(parent["conditions"])
		if ok && currentTrue(conditions, "Accepted", generation) && currentTrue(conditions, "ResolvedRefs", generation) {
			return true
		}
	}
	return false
}

func conditionsFromValue(value any) ([]Condition, bool) {
	return conditionsFromStatus(map[string]any{"conditions": value})
}

func int64Value(value any) (int64, bool) {
	switch v := value.(type) {
	case json.Number:
		parsed, err := v.Int64()
		return parsed, err == nil
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
	}
	return 0, false
}

func applyReason(kind Kind, err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "GATEWAY_PUBLISH_TIMEOUT"
	}
	return "GATEWAY_" + strings.ToUpper(string(kind)) + "_APPLY_FAILED"
}
func statusReason(kind Kind, err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "GATEWAY_STATUS_TIMEOUT"
	}
	return "GATEWAY_" + strings.ToUpper(string(kind)) + "_STATUS_FAILED"
}
func statusStaleReason(kind Kind) string {
	if kind == KindAIGatewayRoute {
		return "GATEWAY_ROUTE_STATUS_STALE"
	}
	return "GATEWAY_" + strings.ToUpper(string(kind)) + "_STATUS_STALE"
}
func deleteReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "GATEWAY_UNPUBLISH_TIMEOUT"
	}
	return "GATEWAY_UNPUBLISH_DELETE_FAILED"
}
func unpublishWaitReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "GATEWAY_UNPUBLISH_TIMEOUT"
	}
	return "GATEWAY_UNPUBLISH_STATUS_FAILED"
}
func stableError(code string, err error) error {
	if err == nil {
		return nil
	}
	return errors.New(code)
}
