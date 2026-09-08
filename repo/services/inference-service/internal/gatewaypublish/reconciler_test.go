package gatewaypublish

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
)

type publicationStoreFake struct {
	target              repository.PublicationTarget
	claimed             bool
	err                 error
	complete            []repository.PublicationResult
	failed              []string
	claims              int
	completeErr         error
	renewErr            error
	renewals            int
	completeHasDeadline bool
	failHasDeadline     bool
}

func (s *publicationStoreFake) ClaimPublication(context.Context, string, time.Time, time.Duration) (repository.PublicationTarget, bool, error) {
	s.claims++
	return s.target, s.claimed, s.err
}
func (s *publicationStoreFake) RenewPublication(_ context.Context, _ repository.PublicationTarget, _ time.Time, _ time.Duration) error {
	s.renewals++
	return s.renewErr
}
func (s *publicationStoreFake) CompletePublication(ctx context.Context, result repository.PublicationResult) error {
	s.complete = append(s.complete, result)
	_, s.completeHasDeadline = ctx.Deadline()
	return s.completeErr
}
func (s *publicationStoreFake) FailPublication(ctx context.Context, _ repository.PublicationTarget, reason string, _ time.Time) error {
	s.failed = append(s.failed, reason)
	_, s.failHasDeadline = ctx.Deadline()
	return nil
}

type kubeFake struct {
	applied         []Kind
	deleted         []Kind
	objects         map[Kind]Object
	applyErr        map[Kind]error
	deleteErr       map[Kind]error
	getErr          map[Kind]error
	keepAfterDelete map[Kind]bool
	getQueue        map[Kind][]Object
}

func (k *kubeFake) Apply(_ context.Context, object Object) (Object, error) {
	k.applied = append(k.applied, object.Kind)
	if err := k.applyErr[object.Kind]; err != nil {
		return Object{}, err
	}
	return k.objects[object.Kind], nil
}
func (k *kubeFake) Get(_ context.Context, kind Kind, _, _ string) (Object, error) {
	if err := k.getErr[kind]; err != nil {
		return Object{}, err
	}
	if pending := k.getQueue[kind]; len(pending) > 0 {
		object := pending[0]
		k.getQueue[kind] = pending[1:]
		return object, nil
	}
	object, ok := k.objects[kind]
	if !ok {
		return Object{}, ErrNotFound
	}
	return object, nil
}
func (k *kubeFake) Delete(_ context.Context, object Object) error {
	k.deleted = append(k.deleted, object.Kind)
	if err := k.deleteErr[object.Kind]; err != nil {
		return err
	}
	if !k.keepAfterDelete[object.Kind] {
		delete(k.objects, object.Kind)
	}
	return nil
}

func reconcilerTarget() repository.PublicationTarget {
	tenantID, serviceID := uuid.New(), uuid.New()
	return repository.PublicationTarget{
		TenantID: tenantID, ServiceID: serviceID, Generation: 7, Desired: domain.PublicationPublished,
		ServedModelName: "ani-qwen3", Task: domain.InferenceTaskGenerate,
		RuntimeEndpoint: "http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local:8000", LeaseToken: uuid.New(),
	}
}

func currentObject(kind Kind, generation int64) Object {
	return Object{Kind: kind, Namespace: "ani-aigw", Name: "ani-aigw", Generation: generation, Status: map[string]any{"conditions": []any{map[string]any{"type": "Accepted", "status": "True", "observedGeneration": generation}}}}
}

func publishObjects(target repository.PublicationTarget) map[Kind]Object {
	objects := map[Kind]Object{
		KindGateway:          {Kind: KindGateway, Namespace: "ani-aigw", Name: "ani-aigw", Generation: 5, Status: map[string]any{"conditions": []any{map[string]any{"type": "Programmed", "status": "True", "observedGeneration": 5}}}},
		KindBackend:          currentObject(KindBackend, 11),
		KindAIServiceBackend: currentObject(KindAIServiceBackend, 12),
	}
	objects[KindAIGatewayRoute] = Object{Kind: KindAIGatewayRoute, Namespace: "ani-aigw", Name: ResourceName(target.ServiceID), Generation: 13, Status: map[string]any{"parents": []any{map[string]any{
		"parentRef":      map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "namespace": "ani-aigw", "name": "ani-aigw"},
		"controllerName": "gateway.envoyproxy.io/gatewayclass-controller",
		"conditions":     []any{map[string]any{"type": "Accepted", "status": "True", "observedGeneration": 13}, map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": 13}},
	}}}}
	return objects
}

func testReconciler(store repository.PublicationStore, kube KubeAPI) *Reconciler {
	return NewReconciler(store, kube, Config{PublicBaseURL: mustURL("https://ai.example.com/prefix"), GatewayNamespace: "ani-aigw", GatewayName: "ani-aigw", GatewayController: "gateway.envoyproxy.io/gatewayclass-controller", RequestTimeout: time.Second, StatusTimeout: 20 * time.Millisecond, LeaseDuration: time.Second}, "test-publisher", func() time.Time { return time.Unix(100, 0) })
}

func mustURL(raw string) *url.URL { u, _ := url.Parse(raw); return u }

func TestPublishAppliesInOrderAndCompletesAfterCurrentConditions(t *testing.T) {
	target := reconcilerTarget()
	store := &publicationStoreFake{target: target, claimed: true}
	kube := &kubeFake{objects: publishObjects(target), applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	claimed, err := testReconciler(store, kube).RunOnce(context.Background())
	if err != nil || !claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	if got := strings.Join([]string{string(kube.applied[0]), string(kube.applied[1]), string(kube.applied[2])}, ","); got != "Backend,AIServiceBackend,AIGatewayRoute" {
		t.Fatalf("apply order = %s", got)
	}
	if len(store.complete) != 1 || store.complete[0].InvocationURL != "https://ai.example.com/prefix/v1/chat/completions" || store.complete[0].Generation != target.Generation {
		t.Fatalf("complete = %+v", store.complete)
	}
	if store.claims != 1 {
		t.Fatalf("claims = %d", store.claims)
	}
	if !store.completeHasDeadline {
		t.Fatal("publication completion did not receive a bounded request context")
	}
}

func TestPublishFailureNeverCompletes(t *testing.T) {
	target := reconcilerTarget()
	store := &publicationStoreFake{target: target, claimed: true}
	kube := &kubeFake{objects: publishObjects(target), applyErr: map[Kind]error{KindAIServiceBackend: errors.New("sensitive endpoint")}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	_, err := testReconciler(store, kube).RunOnce(context.Background())
	if err == nil || len(store.complete) != 0 || len(store.failed) != 1 || store.failed[0] != "GATEWAY_AISERVICEBACKEND_APPLY_FAILED" {
		t.Fatalf("err=%v complete=%v failed=%v", err, store.complete, store.failed)
	}
	if !store.failHasDeadline {
		t.Fatal("publication failure did not receive a bounded request context")
	}
}

func TestPublishRejectsStaleRouteParentAndNeverUsesDatabaseGenerationForKubernetesStatus(t *testing.T) {
	target := reconcilerTarget()
	objects := publishObjects(target)
	objects[KindAIGatewayRoute].Status["parents"].([]any)[0].(map[string]any)["conditions"] = []any{map[string]any{"type": "Accepted", "status": "True", "observedGeneration": target.Generation}, map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": target.Generation}}
	store := &publicationStoreFake{target: target, claimed: true}
	kube := &kubeFake{objects: objects, applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	_, err := testReconciler(store, kube).RunOnce(context.Background())
	if err == nil || len(store.complete) != 0 || len(store.failed) != 1 || store.failed[0] != "GATEWAY_ROUTE_STATUS_STALE" {
		t.Fatalf("err=%v complete=%v failed=%v", err, store.complete, store.failed)
	}
}

func TestPublishRejectsWrongRouteControllerAndTopLevelRouteStatus(t *testing.T) {
	for name, mutate := range map[string]func(Object){
		"wrong controller": func(route Object) {
			route.Status["parents"].([]any)[0].(map[string]any)["controllerName"] = "other-controller"
		},
		"top level only": func(route Object) {
			delete(route.Status, "parents")
			route.Status["conditions"] = []any{map[string]any{"type": "Accepted", "status": "True", "observedGeneration": route.Generation}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			target := reconcilerTarget()
			objects := publishObjects(target)
			mutate(objects[KindAIGatewayRoute])
			store := &publicationStoreFake{target: target, claimed: true}
			kube := &kubeFake{objects: objects, applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
			_, err := testReconciler(store, kube).RunOnce(context.Background())
			if err == nil || len(store.complete) != 0 || len(store.failed) != 1 {
				t.Fatalf("err=%v complete=%v failed=%v", err, store.complete, store.failed)
			}
			if name == "top level only" && store.failed[0] != "GATEWAY_ROUTE_STATUS_UNSUPPORTED" {
				t.Fatalf("reason=%q", store.failed[0])
			}
			if name == "wrong controller" && store.failed[0] != "GATEWAY_ROUTE_STATUS_STALE" {
				t.Fatalf("reason=%q", store.failed[0])
			}
		})
	}
}

func TestUnpublishDeletesRouteBeforeBackendsAndConfirmsAbsence(t *testing.T) {
	target := reconcilerTarget()
	target.Desired = domain.PublicationUnpublished
	store := &publicationStoreFake{target: target, claimed: true}
	kube := &kubeFake{objects: publishObjects(target), applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	claimed, err := testReconciler(store, kube).RunOnce(context.Background())
	if err != nil || !claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	if got := strings.Join([]string{string(kube.deleted[0]), string(kube.deleted[1]), string(kube.deleted[2])}, ","); got != "AIGatewayRoute,AIServiceBackend,Backend" {
		t.Fatalf("delete order = %s", got)
	}
	if len(store.complete) != 1 || store.complete[0].Phase != domain.PublicationUnpublishedOK || store.complete[0].InvocationURL != "" {
		t.Fatalf("complete=%+v", store.complete)
	}
}

func TestUnpublishTimeoutNeverCompletes(t *testing.T) {
	target := reconcilerTarget()
	target.Desired = domain.PublicationUnpublished
	store := &publicationStoreFake{target: target, claimed: true}
	objects := publishObjects(target)
	kube := &kubeFake{objects: objects, applyErr: map[Kind]error{}, deleteErr: map[Kind]error{KindAIGatewayRoute: context.DeadlineExceeded}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	_, err := testReconciler(store, kube).RunOnce(context.Background())
	if err == nil || len(store.complete) != 0 || len(store.failed) != 1 || store.failed[0] != "GATEWAY_UNPUBLISH_TIMEOUT" {
		t.Fatalf("err=%v complete=%v failed=%v", err, store.complete, store.failed)
	}
}

func TestCurrentConditionJSONUsesStrictInt64AndRejectsDuplicates(t *testing.T) {
	decoded, err := decodeObject([]byte(`{"apiVersion":"gateway.networking.k8s.io/v1","kind":"Gateway","metadata":{"name":"ani-aigw","namespace":"ani-aigw","generation":5},"status":{"conditions":[{"type":"Programmed","status":"True","observedGeneration":5}]}}`), Object{Kind: KindGateway, Namespace: "ani-aigw", Name: "ani-aigw"})
	if err != nil {
		t.Fatal(err)
	}
	conditions, ok := conditionsFromStatus(decoded.Status)
	if !ok || !currentTrue(conditions, "Programmed", decoded.Generation) {
		t.Fatalf("conditions=%+v ok=%v", conditions, ok)
	}
	if currentTrue(append(conditions, conditions[0]), "Programmed", decoded.Generation) {
		t.Fatal("duplicate current condition accepted")
	}
	if _, ok := int64Value(json.Number("5.1")); ok {
		t.Fatal("fractional JSON number accepted")
	}
}

func TestRouteParentAllowsImplicitNamespaceAndConfiguredController(t *testing.T) {
	target := reconcilerTarget()
	objects := publishObjects(target)
	parent := objects[KindAIGatewayRoute].Status["parents"].([]any)[0].(map[string]any)
	delete(parent["parentRef"].(map[string]any), "namespace")
	parent["controllerName"] = "configured-controller"
	store := &publicationStoreFake{target: target, claimed: true}
	kube := &kubeFake{objects: objects, applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	r := testReconciler(store, kube)
	r.gatewayController = "configured-controller"
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPublishPollsStaleStatusUntilCurrent(t *testing.T) {
	target := reconcilerTarget()
	objects := publishObjects(target)
	stale := currentObject(KindBackend, 11)
	stale.Status["conditions"] = []any{map[string]any{"type": "Accepted", "status": "True", "observedGeneration": 10}}
	current := currentObject(KindBackend, 11)
	store := &publicationStoreFake{target: target, claimed: true}
	kube := &kubeFake{objects: objects, applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{KindBackend: {stale, current}}}
	r := testReconciler(store, kube)
	r.statusTimeout = 300 * time.Millisecond
	if _, err := r.RunOnce(context.Background()); err != nil || len(store.complete) != 1 {
		t.Fatalf("err=%v complete=%v", err, store.complete)
	}
}

func TestPublishRenewsLeaseDuringStatusPollingAndBeforeCompletion(t *testing.T) {
	target := reconcilerTarget()
	objects := publishObjects(target)
	stale := currentObject(KindBackend, 11)
	stale.Status["conditions"] = []any{map[string]any{"type": "Accepted", "status": "True", "observedGeneration": 10}}
	current := currentObject(KindBackend, 11)
	store := &publicationStoreFake{target: target, claimed: true}
	kube := &kubeFake{objects: objects, applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{KindBackend: {stale, current}}}
	r := NewReconciler(store, kube, Config{PublicBaseURL: mustURL("https://ai.example.com"), GatewayNamespace: "ani-aigw", GatewayName: "ani-aigw", GatewayController: "gateway.envoyproxy.io/gatewayclass-controller", RequestTimeout: 5 * time.Millisecond, StatusTimeout: 100 * time.Millisecond, LeaseDuration: 15 * time.Millisecond}, "test-publisher", time.Now)

	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.renewals < 2 {
		t.Fatalf("renewals = %d, want polling renewal plus completion fence", store.renewals)
	}
}

func TestPublishStopsBeforeKubernetesMutationWhenLeaseRenewalFails(t *testing.T) {
	target := reconcilerTarget()
	store := &publicationStoreFake{target: target, claimed: true, renewErr: repository.ErrStaleGeneration}
	kube := &kubeFake{objects: publishObjects(target), applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	r := NewReconciler(store, kube, Config{PublicBaseURL: mustURL("https://ai.example.com"), GatewayNamespace: "ani-aigw", GatewayName: "ani-aigw", GatewayController: "gateway.envoyproxy.io/gatewayclass-controller", RequestTimeout: time.Second, StatusTimeout: time.Second, LeaseDuration: time.Second}, "test-publisher", time.Now)

	if _, err := r.RunOnce(context.Background()); err == nil || err.Error() != "GATEWAY_LEASE_RENEW_FAILED" {
		t.Fatalf("err = %v, want stable lease renewal failure", err)
	}
	if len(kube.applied) != 0 || len(store.failed) != 0 || len(store.complete) != 0 {
		t.Fatalf("applied=%v failed=%v complete=%v", kube.applied, store.failed, store.complete)
	}
}

func TestUnpublishConfirmsEachObjectBeforeComplete(t *testing.T) {
	target := reconcilerTarget()
	target.Desired = domain.PublicationUnpublished
	store := &publicationStoreFake{target: target, claimed: true}
	objects := publishObjects(target)
	kube := &kubeFake{objects: objects, applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, getErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{KindAIServiceBackend: true}, getQueue: map[Kind][]Object{}}
	r := testReconciler(store, kube)
	r.statusTimeout = 25 * time.Millisecond
	if _, err := r.RunOnce(context.Background()); err == nil || len(store.complete) != 0 || store.failed[0] != "GATEWAY_UNPUBLISH_TIMEOUT" {
		t.Fatalf("complete=%v failed=%v", store.complete, store.failed)
	}
}

func TestUnknownDesiredFailsWithoutKubernetesMutation(t *testing.T) {
	target := reconcilerTarget()
	target.Desired = "unknown"
	store := &publicationStoreFake{target: target, claimed: true}
	kube := &kubeFake{objects: publishObjects(target), applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	if _, err := testReconciler(store, kube).RunOnce(context.Background()); err == nil || len(kube.applied) != 0 || len(kube.deleted) != 0 || len(store.complete) != 0 || store.failed[0] != "GATEWAY_DESIRED_INVALID" {
		t.Fatalf("applied=%v deleted=%v complete=%v failed=%v", kube.applied, kube.deleted, store.complete, store.failed)
	}
}

func TestLateGenerationOrStaleLeaseCompletionNeverRecordsFailure(t *testing.T) {
	target := reconcilerTarget()
	store := &publicationStoreFake{target: target, claimed: true, completeErr: errors.New("stale lease token")}
	kube := &kubeFake{objects: publishObjects(target), applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, keepAfterDelete: map[Kind]bool{}, getQueue: map[Kind][]Object{}}
	_, err := testReconciler(store, kube).RunOnce(context.Background())
	if err == nil || err.Error() != "GATEWAY_COMPLETE_FAILED" || len(store.complete) != 1 || len(store.failed) != 0 {
		t.Fatalf("err=%v complete=%v failed=%v", err, store.complete, store.failed)
	}
}

func TestUnpublishGetFailureDoesNotPollOrComplete(t *testing.T) {
	target := reconcilerTarget()
	target.Desired = domain.PublicationUnpublished
	store := &publicationStoreFake{target: target, claimed: true}
	objects := publishObjects(target)
	kube := &kubeFake{objects: objects, applyErr: map[Kind]error{}, deleteErr: map[Kind]error{}, getErr: map[Kind]error{KindAIServiceBackend: errors.New("get unavailable")}, keepAfterDelete: map[Kind]bool{KindAIServiceBackend: true}, getQueue: map[Kind][]Object{}}
	_, err := testReconciler(store, kube).RunOnce(context.Background())
	if err == nil || len(store.complete) != 0 || len(store.failed) != 1 || store.failed[0] != "GATEWAY_UNPUBLISH_STATUS_FAILED" {
		t.Fatalf("err=%v complete=%v failed=%v", err, store.complete, store.failed)
	}
}
