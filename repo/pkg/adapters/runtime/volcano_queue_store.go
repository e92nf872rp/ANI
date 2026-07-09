package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// Volcano Queue CRD constants (scheduling.volcano.sh/v1beta1).
const (
	volcanoQueueAPIGroup   = "scheduling.volcano.sh"
	volcanoQueueAPIVersion = "v1beta1"
	volcanoQueueResource   = "queues"
	volcanoQueueKind       = "Queue"

	// Label keys stamped onto every Volcano Queue CRD managed by ANI.
	volcanoLabelTenantID        = "ani.kubercloud.io/tenant"
	volcanoLabelWorkloadClass   = "ani.kubercloud.io/workload-class"
	volcanoLabelQueueID         = "ani.kubercloud.io/queue-id"
	volcanoLabelPlatformDefault = "ani.kubercloud.io/platform-default"
	volcanoLabelProjectID       = "ani.kubercloud.io/project-id"
)

// VolcanoHTTPDoer is the minimal K8s REST surface the adapter needs.
// The gateway runtime supplies a production implementation backed by
// KubernetesRESTClient; tests use DoerFunc.
type VolcanoHTTPDoer interface {
	Do(ctx context.Context, method, endpoint, contentType string, body []byte) ([]byte, int, error)
}

// DoerFunc adapts a function to VolcanoHTTPDoer for testing.
type DoerFunc func(ctx context.Context, method, endpoint, contentType string, body []byte) ([]byte, int, error)

func (f DoerFunc) Do(ctx context.Context, method, endpoint, contentType string, body []byte) ([]byte, int, error) {
	return f(ctx, method, endpoint, contentType, body)
}

// VolcanoQueueStore implements ports.GPUSchedulingQueueStore over Volcano
// Queue CRD. Data persists in K8s etcd; no PostgreSQL involved.
type VolcanoQueueStore struct {
	doer                   VolcanoHTTPDoer
	baseURL                string
	namespace              string
	now                    func() time.Time
	ensurePlatformDefaults bool
	initOnce               sync.Once
}

// VolcanoQueueStoreConfig configures the adapter.
type VolcanoQueueStoreConfig struct {
	// Doer performs K8s REST calls. When nil the adapter returns
	// ErrQueueStoreUnavailable on every call.
	Doer VolcanoHTTPDoer
	// BaseURL is the Kubernetes API host (e.g. https://kubernetes.default.svc).
	BaseURL string
	// Namespace is the Volcano Queue CRD namespace. Default "volcano-system".
	Namespace string
	// Now supplies the current time. Defaults to time.Now.
	Now func() time.Time
	// EnsurePlatformDefaults, when true, creates the two platform-default
	// queues (ani-inference / ani-training) if they do not already exist in
	// the K8s cluster. Defaults to false (no auto-creation).
	EnsurePlatformDefaults bool
}

// NewVolcanoQueueStore builds a VolcanoQueueStore adapter.
func NewVolcanoQueueStore(cfg VolcanoQueueStoreConfig) *VolcanoQueueStore {
	if strings.TrimSpace(cfg.Namespace) == "" {
		cfg.Namespace = "volcano-system"
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &VolcanoQueueStore{
		doer:                   cfg.Doer,
		baseURL:                strings.TrimRight(cfg.BaseURL, "/"),
		namespace:              cfg.Namespace,
		now:                    now,
		ensurePlatformDefaults: cfg.EnsurePlatformDefaults,
	}
}

// platformDefaults are the two mandatory platform-default queues.
type platformDefaultQueue struct {
	name        string
	workload    ports.WorkloadClass
	weight      int
	reclaimable bool
}

var platformDefaults = []platformDefaultQueue{
	{
		name:        "ani-inference",
		workload:    ports.WorkloadClassInference,
		weight:      10,
		reclaimable: false,
	},
	{
		name:        "ani-training",
		workload:    ports.WorkloadClassTraining,
		weight:      5,
		reclaimable: true,
	},
}

// EnsurePlatformQueueDefaults creates the two platform-default queues
// (ani-inference / ani-training) in the K8s cluster if they don't already
// exist. This mirrors the behaviour of LocalGPUSchedulingQueueStore.seedDefaults.
// Returns silently if the queues already exist.
func (s *VolcanoQueueStore) EnsurePlatformQueueDefaults(ctx context.Context) error {
	if !s.ensurePlatformDefaults {
		return nil
	}
	for _, def := range platformDefaults {
		_, err := s.getCRDByName(ctx, def.name)
		if err == nil {
			continue // already exists
		}
		if !errors.Is(err, ports.ErrQueueNotFound) {
			// Non-404 error; log but don't block
			continue
		}
		body, err := json.Marshal(volcanoQueueCRD{
			APIVersion: volcanoQueueAPIGroup + "/" + volcanoQueueAPIVersion,
			Kind:       volcanoQueueKind,
			Metadata: volcanoQueueCRDMeta{
				Name: def.name,
				Labels: map[string]string{
					volcanoLabelWorkloadClass:   string(def.workload),
					volcanoLabelPlatformDefault: "true",
				},
			},
			Spec: volcanoQueueCRDSpec{
				Weight:      def.weight,
				Reclaimable: def.reclaimable,
			},
		})
		if err != nil {
			continue
		}
		_, _, err = s.doer.Do(ctx, http.MethodPost, s.collectionURL(""), "application/json", body)
		_ = err // creation may race; next attempt will find it
	}
	return nil
}

// volcanoQueueCRD is the minimal Volcano Queue CRD JSON shape this adapter reads/writes.
type volcanoQueueCRD struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   volcanoQueueCRDMeta `json:"metadata"`
	Spec       volcanoQueueCRDSpec `json:"spec,omitempty"`
}

type volcanoQueueCRDMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	UID             string            `json:"uid,omitempty"`
}

type volcanoQueueCRDSpec struct {
	Weight      int  `json:"weight,omitempty"`
	Reclaimable bool `json:"reclaimable,omitempty"`
}

// volcanoQueueListCRD is the list response from K8s API.
type volcanoQueueListCRD struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Items      []volcanoQueueCRD `json:"items"`
}

func (s *VolcanoQueueStore) List(ctx context.Context, tenantID string) ([]ports.GPUSchedulingQueue, error) {
	if s.doer == nil {
		return nil, ports.ErrQueueStoreUnavailable
	}
	s.initOnce.Do(func() { _ = s.EnsurePlatformQueueDefaults(ctx) })
	// Volcano Queue CRD is cluster-scoped. We fetch all queues and then
	// filter: platform-default queues are visible to all tenants; custom
	// queues are filtered by tenant ID.
	endpoint := s.collectionURL("")
	body, _, err := s.doer.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return nil, mapK8sError(err)
	}
	var list volcanoQueueListCRD
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode queue list: %v", ports.ErrInvalid, err)
	}
	queues := make([]ports.GPUSchedulingQueue, 0, len(list.Items))
	for _, item := range list.Items {
		isPlatformDefault := isPlatformDefaultCRD(item)
		isTenantQueue := item.Metadata.Labels[volcanoLabelTenantID] == tenantID
		if isPlatformDefault || isTenantQueue {
			queues = append(queues, s.crdToQueue(item))
		}
	}
	return queues, nil
}

func (s *VolcanoQueueStore) Get(ctx context.Context, tenantID, id string) (ports.GPUSchedulingQueue, error) {
	if s.doer == nil {
		return ports.GPUSchedulingQueue{}, ports.ErrQueueStoreUnavailable
	}
	crd, err := s.lookupQueueByNameOrID(ctx, tenantID, id)
	if err != nil {
		return ports.GPUSchedulingQueue{}, err
	}
	return s.crdToQueue(crd), nil
}

func (s *VolcanoQueueStore) Create(ctx context.Context, tenantID string, req ports.GPUSchedulingQueueCreateRequest) (ports.GPUSchedulingQueue, error) {
	if s.doer == nil {
		return ports.GPUSchedulingQueue{}, ports.ErrQueueStoreUnavailable
	}
	if err := validateQueueName(req.Name); err != nil {
		return ports.GPUSchedulingQueue{}, err
	}
	existing, err := s.List(ctx, tenantID)
	if err != nil {
		return ports.GPUSchedulingQueue{}, err
	}
	for _, q := range existing {
		if q.Name == req.Name {
			return ports.GPUSchedulingQueue{}, ports.ErrQueueNameConflict
		}
	}
	queueID := uuid.New().String()
	now := s.now().UTC()
	crd := s.queueToCRD(tenantID, ports.GPUSchedulingQueue{
		ID:            queueID,
		Name:          req.Name,
		Weight:        req.Weight,
		Reclaimable:   req.Reclaimable,
		WorkloadClass: req.WorkloadClass,
		ProjectID:     req.ProjectID,
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	body, err := json.Marshal(crd)
	if err != nil {
		return ports.GPUSchedulingQueue{}, fmt.Errorf("%w: marshal queue CRD: %v", ports.ErrInvalid, err)
	}
	respBody, status, err := s.doer.Do(ctx, http.MethodPost, s.collectionURL(""), "application/json", body)
	if err != nil {
		return ports.GPUSchedulingQueue{}, mapK8sError(err)
	}
	if status == http.StatusConflict {
		return ports.GPUSchedulingQueue{}, ports.ErrQueueNameConflict
	}
	if status < 200 || status >= 300 {
		return ports.GPUSchedulingQueue{}, fmt.Errorf("%w: create queue HTTP %d: %s", ports.ErrInvalid, status, string(respBody))
	}
	var created volcanoQueueCRD
	if err := json.Unmarshal(respBody, &created); err != nil {
		return ports.GPUSchedulingQueue{}, fmt.Errorf("%w: decode created queue: %v", ports.ErrInvalid, err)
	}
	return s.crdToQueue(created), nil
}

func (s *VolcanoQueueStore) Update(ctx context.Context, tenantID, id string, req ports.GPUSchedulingQueueUpdateRequest) (ports.GPUSchedulingQueue, error) {
	if s.doer == nil {
		return ports.GPUSchedulingQueue{}, ports.ErrQueueStoreUnavailable
	}
	// Resolve queue by name (queue ID is looked up from label first).
	crd, err := s.lookupQueueByNameOrID(ctx, tenantID, id)
	if err != nil {
		return ports.GPUSchedulingQueue{}, err
	}
	if isPlatformDefaultCRD(crd) {
		return ports.GPUSchedulingQueue{}, ports.ErrPlatformDefaultProtected
	}
	// For non-default queues, ensure tenant ownership.
	if crd.Metadata.Labels[volcanoLabelTenantID] != tenantID {
		return ports.GPUSchedulingQueue{}, ports.ErrQueueNotFound
	}
	if req.Weight != nil {
		crd.Spec.Weight = *req.Weight
	}
	if req.Reclaimable != nil {
		crd.Spec.Reclaimable = *req.Reclaimable
	}
	if req.WorkloadClass != nil {
		if crd.Metadata.Labels == nil {
			crd.Metadata.Labels = map[string]string{}
		}
		crd.Metadata.Labels[volcanoLabelWorkloadClass] = string(*req.WorkloadClass)
	}
	if req.ProjectID != nil {
		if crd.Metadata.Labels == nil {
			crd.Metadata.Labels = map[string]string{}
		}
		crd.Metadata.Labels[volcanoLabelProjectID] = *req.ProjectID
	}
	if crd.Metadata.Annotations == nil {
		crd.Metadata.Annotations = map[string]string{}
	}
	crd.Metadata.Annotations["ani.kubercloud.io/updated-at"] = s.now().UTC().Format(time.RFC3339)
	body, err := json.Marshal(crd)
	if err != nil {
		return ports.GPUSchedulingQueue{}, fmt.Errorf("%w: marshal queue CRD: %v", ports.ErrInvalid, err)
	}
	endpoint := s.resourceURL(crd.Metadata.Name)
	respBody, status, err := s.doer.Do(ctx, http.MethodPut, endpoint, "application/json", body)
	if err != nil {
		return ports.GPUSchedulingQueue{}, mapK8sError(err)
	}
	if status == http.StatusNotFound {
		return ports.GPUSchedulingQueue{}, ports.ErrQueueNotFound
	}
	if status < 200 || status >= 300 {
		return ports.GPUSchedulingQueue{}, fmt.Errorf("%w: update queue HTTP %d: %s", ports.ErrInvalid, status, string(respBody))
	}
	var updated volcanoQueueCRD
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return ports.GPUSchedulingQueue{}, fmt.Errorf("%w: decode updated queue: %v", ports.ErrInvalid, err)
	}
	return s.crdToQueue(updated), nil
}

func (s *VolcanoQueueStore) Delete(ctx context.Context, tenantID, id string) error {
	if s.doer == nil {
		return ports.ErrQueueStoreUnavailable
	}
	crd, err := s.lookupQueueByNameOrID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if isPlatformDefaultCRD(crd) {
		return ports.ErrPlatformDefaultProtected
	}
	endpoint := s.resourceURL(crd.Metadata.Name)
	_, status, err := s.doer.Do(ctx, http.MethodDelete, endpoint, "", nil)
	if err != nil {
		return mapK8sError(err)
	}
	if status == http.StatusNotFound {
		return ports.ErrQueueNotFound
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%w: delete queue HTTP %d", ports.ErrInvalid, status)
	}
	return nil
}

// getCRDByID finds the Volcano Queue CRD by queue ID (stored in label).
// Returns ErrQueueNotFound when the CRD doesn't exist or belongs to another tenant.
func (s *VolcanoQueueStore) getCRDByID(ctx context.Context, tenantID, id string) (volcanoQueueCRD, error) {
	endpoint := s.collectionURL(labelSelectorTenant(tenantID) + "," + labelSelectorQueueID(id))
	body, _, err := s.doer.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return volcanoQueueCRD{}, mapK8sError(err)
	}
	var list volcanoQueueListCRD
	if err := json.Unmarshal(body, &list); err != nil {
		return volcanoQueueCRD{}, fmt.Errorf("%w: decode queue list: %v", ports.ErrInvalid, err)
	}
	if len(list.Items) == 0 {
		return volcanoQueueCRD{}, ports.ErrQueueNotFound
	}
	return list.Items[0], nil
}

// lookupQueueByNameOrID resolves a queue by either its ANI-assigned ID or its
// Volcano CRD name. It checks tenant ownership for non-default queues.
func (s *VolcanoQueueStore) lookupQueueByNameOrID(ctx context.Context, tenantID, idOrName string) (volcanoQueueCRD, error) {
	// Try resolving by queue ID first (ANI-assigned label).
	crd, err := s.getCRDByID(ctx, tenantID, idOrName)
	if err == nil {
		return crd, nil
	}
	// If not found by ID, try resolving by CRD name.
	crd, err = s.getCRDByName(ctx, idOrName)
	if err != nil {
		return volcanoQueueCRD{}, ports.ErrQueueNotFound
	}
	// For non-default queues, verify tenant ownership.
	if !isPlatformDefaultCRD(crd) && crd.Metadata.Labels[volcanoLabelTenantID] != tenantID {
		return volcanoQueueCRD{}, ports.ErrQueueNotFound
	}
	return crd, nil
}

// getCRDByName finds a Volcano Queue CRD by its name directly.
func (s *VolcanoQueueStore) getCRDByName(ctx context.Context, name string) (volcanoQueueCRD, error) {
	endpoint := s.resourceURL(name)
	body, status, err := s.doer.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return volcanoQueueCRD{}, mapK8sError(err)
	}
	if status == http.StatusNotFound {
		return volcanoQueueCRD{}, ports.ErrQueueNotFound
	}
	var crd volcanoQueueCRD
	if err := json.Unmarshal(body, &crd); err != nil {
		return volcanoQueueCRD{}, fmt.Errorf("%w: decode queue CRD: %v", ports.ErrInvalid, err)
	}
	return crd, nil
}

func (s *VolcanoQueueStore) collectionURL(labelSelector string) string {
	// Volcano Queue CRD is cluster-scoped (scope=Cluster), so the REST path
	// omits the namespaces segment: /apis/{group}/{version}/{resource}
	base := fmt.Sprintf("%s/apis/%s/%s/%s",
		s.baseURL, volcanoQueueAPIGroup, volcanoQueueAPIVersion, volcanoQueueResource)
	if labelSelector != "" {
		base += "?labelSelector=" + labelSelector
	}
	return base
}

func (s *VolcanoQueueStore) resourceURL(name string) string {
	return fmt.Sprintf("%s/apis/%s/%s/%s/%s",
		s.baseURL, volcanoQueueAPIGroup, volcanoQueueAPIVersion, volcanoQueueResource, name)
}

func (s *VolcanoQueueStore) crdToQueue(crd volcanoQueueCRD) ports.GPUSchedulingQueue {
	labels := crd.Metadata.Labels
	createdAt, _ := time.Parse(time.RFC3339, firstNonEmptyLabel(labels, "ani.kubercloud.io/created-at"))
	updatedAt := createdAt
	if raw, ok := crd.Metadata.Annotations["ani.kubercloud.io/updated-at"]; ok {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			updatedAt = t
		}
	}
	if createdAt.IsZero() {
		createdAt = s.now().UTC()
		updatedAt = createdAt
	}
	queueID := labels[volcanoLabelQueueID]
	if queueID == "" {
		queueID = strings.ReplaceAll(strings.TrimSpace(crd.Metadata.UID), "-", "")[:16]
	}
	return ports.GPUSchedulingQueue{
		ID:                queueID,
		Name:              crd.Metadata.Name,
		Weight:            crd.Spec.Weight,
		Reclaimable:       crd.Spec.Reclaimable,
		WorkloadClass:     ports.WorkloadClass(labels[volcanoLabelWorkloadClass]),
		ProjectID:         labels[volcanoLabelProjectID],
		IsPlatformDefault: isPlatformDefaultCRD(crd),
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}
}

func firstNonEmptyLabel(labels map[string]string, key string) string {
	if v, ok := labels[key]; ok {
		return v
	}
	return ""
}

func (s *VolcanoQueueStore) queueToCRD(tenantID string, q ports.GPUSchedulingQueue) volcanoQueueCRD {
	labels := map[string]string{
		volcanoLabelTenantID:      tenantID,
		volcanoLabelWorkloadClass: string(q.WorkloadClass),
		volcanoLabelQueueID:       q.ID,
	}
	if q.IsPlatformDefault {
		labels[volcanoLabelPlatformDefault] = "true"
	}
	if q.ProjectID != "" {
		labels[volcanoLabelProjectID] = q.ProjectID
	}
	annotations := map[string]string{
		"ani.kubercloud.io/created-at": q.CreatedAt.Format(time.RFC3339),
		"ani.kubercloud.io/updated-at": q.UpdatedAt.Format(time.RFC3339),
	}
	return volcanoQueueCRD{
		APIVersion: volcanoQueueAPIGroup + "/" + volcanoQueueAPIVersion,
		Kind:       volcanoQueueKind,
		Metadata: volcanoQueueCRDMeta{
			Name:        q.Name,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: volcanoQueueCRDSpec{
			Weight:      q.Weight,
			Reclaimable: q.Reclaimable,
		},
	}
}

func isPlatformDefaultCRD(crd volcanoQueueCRD) bool {
	if crd.Metadata.Labels[volcanoLabelPlatformDefault] == "true" {
		return true
	}
	// Also treat known default queue names without an ANI tenant label as platform defaults.
	if crd.Metadata.Labels[volcanoLabelTenantID] == "" {
		for _, def := range platformDefaults {
			if crd.Metadata.Name == def.name {
				return true
			}
		}
	}
	return false
}

func labelSelectorTenant(tenantID string) string {
	return fmt.Sprintf("%s=%s", volcanoLabelTenantID, tenantID)
}

func labelSelectorQueueID(id string) string {
	return fmt.Sprintf("%s=%s", volcanoLabelQueueID, id)
}

// validateQueueName enforces K8s resource name convention:
// ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$, 1-63 characters.
func validateQueueName(name string) error {
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 63 {
		return fmt.Errorf("%w: queue name must be 1-63 characters", ports.ErrInvalid)
	}
	for i, r := range name {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '-' && i != 0 && i != len(name)-1 {
			continue
		}
		return fmt.Errorf("%w: queue name must match ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", ports.ErrInvalid)
	}
	return nil
}

// mapK8sError translates K8s REST errors into ports errors.
func mapK8sError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "404") || strings.Contains(msg, "not found") {
		return ports.ErrQueueNotFound
	}
	if strings.Contains(msg, "409") || strings.Contains(msg, "conflict") {
		return ports.ErrQueueNameConflict
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "unavailable") {
		return ports.ErrQueueStoreUnavailable
	}
	return fmt.Errorf("%w: %v", ports.ErrInvalid, err)
}

var _ ports.GPUSchedulingQueueStore = (*VolcanoQueueStore)(nil)
