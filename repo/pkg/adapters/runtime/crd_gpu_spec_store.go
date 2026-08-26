package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// GPUSpec CRD constants (ani.kubercloud.io/v1). The CRD is cluster-scoped;
// spec_id is the CRD metadata.name and doubles as the public id.
const (
	gpuSpecAPIGroup   = "ani.kubercloud.io"
	gpuSpecAPIVersion = "v1"
	gpuSpecResource   = "gpuspecs"
	gpuSpecKind       = "GPUSpec"
	gpuSpecListKind   = "GPUSpecList"

	// Label key stamped onto every GPUSpec CRD for idempotency replay.
	gpuSpecLabelIdempotencyKey = "ani.kubercloud.io/idempotency-key"
)

// CRDGPUSpecStore implements ports.GPUSpecStore over the GPUSpec CRD. It
// follows the CRD operation mode established by volcano_queue_store.go:
// specs are cluster-scoped custom resources, idempotency is replayed via a
// K8s label, and the K8s REST surface is abstracted by VolcanoHTTPDoer so
// tests can inject a fake API.
type CRDGPUSpecStore struct {
	doer    VolcanoHTTPDoer
	baseURL string
}

// CRDGPUSpecStoreConfig configures the adapter.
type CRDGPUSpecStoreConfig struct {
	// Doer performs K8s REST calls. When nil the adapter returns
	// ports.ErrGPUSpecNotFound on every call (graceful degradation when
	// the CRD controller is not installed).
	Doer VolcanoHTTPDoer
	// BaseURL is the Kubernetes API host (e.g. https://kubernetes.default.svc).
	BaseURL string
}

// NewCRDGPUSpecStore builds a CRDGPUSpecStore adapter.
func NewCRDGPUSpecStore(cfg CRDGPUSpecStoreConfig) *CRDGPUSpecStore {
	return &CRDGPUSpecStore{
		doer:    cfg.Doer,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
	}
}

// gpuSpecCRD is the minimal GPUSpec CRD JSON shape this adapter reads/writes.
type gpuSpecCRD struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   gpuSpecCRDMeta `json:"metadata"`
	Spec       gpuSpecCRDSpec `json:"spec"`
}

type gpuSpecCRDMeta struct {
	Name            string            `json:"name"`
	Labels          map[string]string `json:"labels,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	UID             string            `json:"uid,omitempty"`
}

type gpuSpecCRDSpec struct {
	Name             string                     `json:"name,omitempty"`
	GPUType          string                     `json:"gpu_type"`
	GPUMode          string                     `json:"gpu_mode"`
	MemoryTotalMB    int64                      `json:"memory_total_mb,omitempty"`
	Shares           int                        `json:"shares"`
	MBPerShare       int                        `json:"mb_per_share"`
	Available        *bool                      `json:"available,omitempty"`
	ComputePerShare  *int64                     `json:"compute_per_share,omitempty"`
	NodeAffinity     gpuSpecCRDNodeAffinity     `json:"node_affinity"`
	VolcanoResources gpuSpecCRDVolcanoResources `json:"volcano_resources"`
}

type gpuSpecCRDNodeAffinity struct {
	GPUSpec          string `json:"gpu_spec"`
	GPUSharingSpec   string `json:"gpu_sharing_spec"`
	GPUSharingPolicy string `json:"gpu_sharing_policy"`
	GPUMode          string `json:"gpu_mode"`
}

type gpuSpecCRDVolcanoResources struct {
	Wholecard map[string]string `json:"wholecard,omitempty"`
	VGPU      map[string]string `json:"vgpu,omitempty"`
}

// gpuSpecListCRD is the list response from K8s API.
type gpuSpecListCRD struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Items      []gpuSpecCRD `json:"items"`
}

// List returns all GPUSpec custom resources in the cluster.
func (s *CRDGPUSpecStore) List(ctx context.Context) ([]ports.GPUSpecCRD, error) {
	if s.doer == nil {
		return nil, ports.ErrGPUSpecNotFound
	}
	endpoint := s.collectionURL("")
	body, status, err := s.doer.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return nil, mapGPUSpecK8sError(err)
	}
	if status == http.StatusNotFound {
		return nil, ports.ErrGPUSpecNotFound
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("%w: list gpu specs HTTP %d: %s", ports.ErrGPUSpecNotFound, status, string(body))
	}
	var list gpuSpecListCRD
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode gpu spec list: %v", ports.ErrGPUSpecNotFound, err)
	}
	specs := make([]ports.GPUSpecCRD, 0, len(list.Items))
	for _, item := range list.Items {
		specs = append(specs, crdToGPUSpec(item))
	}
	return specs, nil
}

// Get returns a single GPUSpec by spec_id (CRD metadata.name).
func (s *CRDGPUSpecStore) Get(ctx context.Context, specID string) (ports.GPUSpecCRD, error) {
	if s.doer == nil {
		return ports.GPUSpecCRD{}, ports.ErrGPUSpecNotFound
	}
	if err := validateGPUSpecID(specID); err != nil {
		return ports.GPUSpecCRD{}, err
	}
	endpoint := s.resourceURL(specID)
	body, status, err := s.doer.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return ports.GPUSpecCRD{}, mapGPUSpecK8sError(err)
	}
	if status == http.StatusNotFound {
		return ports.GPUSpecCRD{}, ports.ErrGPUSpecNotFound
	}
	if status < 200 || status >= 300 {
		return ports.GPUSpecCRD{}, fmt.Errorf("%w: get gpu spec HTTP %d: %s", ports.ErrGPUSpecNotFound, status, string(body))
	}
	var crd gpuSpecCRD
	if err := json.Unmarshal(body, &crd); err != nil {
		return ports.GPUSpecCRD{}, fmt.Errorf("%w: decode gpu spec: %v", ports.ErrGPUSpecNotFound, err)
	}
	return crdToGPUSpec(crd), nil
}

// Create persists a new GPUSpec CRD. When idempotencyKey is non-empty it is
// stamped as a K8s label; a duplicate request with the same key replays the
// existing CRD instead of returning a conflict.
func (s *CRDGPUSpecStore) Create(ctx context.Context, idempotencyKey string, spec ports.GPUSpecCRD) (ports.GPUSpecCRD, error) {
	if s.doer == nil {
		return ports.GPUSpecCRD{}, ports.ErrGPUSpecNotFound
	}
	if err := validateGPUSpecID(spec.ID); err != nil {
		return ports.GPUSpecCRD{}, err
	}
	// Idempotency replay: check if a spec with this idempotency_key already exists.
	if idempotencyKey != "" {
		if existing, err := s.findByLabel(ctx, gpuSpecLabelIdempotencyKey, idempotencyKey); err == nil {
			return crdToGPUSpec(existing), nil
		}
	}
	// Conflict check: spec_id (metadata.name) must be unique.
	if _, err := s.getCRDByName(ctx, spec.ID); err == nil {
		return ports.GPUSpecCRD{}, ports.ErrGPUSpecConflict
	}
	crd := gpuSpecToCRD(spec)
	if idempotencyKey != "" {
		if crd.Metadata.Labels == nil {
			crd.Metadata.Labels = map[string]string{}
		}
		crd.Metadata.Labels[gpuSpecLabelIdempotencyKey] = idempotencyKey
	}
	body, err := json.Marshal(crd)
	if err != nil {
		return ports.GPUSpecCRD{}, fmt.Errorf("%w: marshal gpu spec CRD: %v", ports.ErrGPUSpecConflict, err)
	}
	respBody, status, err := s.doer.Do(ctx, http.MethodPost, s.collectionURL(""), "application/json", body)
	if err != nil {
		return ports.GPUSpecCRD{}, mapGPUSpecK8sError(err)
	}
	if status == http.StatusConflict {
		return ports.GPUSpecCRD{}, ports.ErrGPUSpecConflict
	}
	if status < 200 || status >= 300 {
		return ports.GPUSpecCRD{}, fmt.Errorf("%w: create gpu spec HTTP %d: %s", ports.ErrGPUSpecConflict, status, string(respBody))
	}
	var created gpuSpecCRD
	if err := json.Unmarshal(respBody, &created); err != nil {
		return ports.GPUSpecCRD{}, fmt.Errorf("%w: decode created gpu spec: %v", ports.ErrGPUSpecConflict, err)
	}
	return crdToGPUSpec(created), nil
}

// Delete removes a GPUSpec CRD by spec_id. Per SPEC §5.2, the adapter does
// NOT query PostgreSQL for workload_instances references; it returns the
// spec so the handler layer can perform the in-use check. The adapter only
// validates that the spec exists and delegates deletion to the K8s API.
func (s *CRDGPUSpecStore) Delete(ctx context.Context, idempotencyKey string, specID string) error {
	if s.doer == nil {
		return ports.ErrGPUSpecNotFound
	}
	if err := validateGPUSpecID(specID); err != nil {
		return err
	}
	// Ensure the spec exists before attempting deletion.
	if _, err := s.getCRDByName(ctx, specID); err != nil {
		return err
	}
	endpoint := s.resourceURL(specID)
	respBody, status, err := s.doer.Do(ctx, http.MethodDelete, endpoint, "", nil)
	if err != nil {
		return mapGPUSpecK8sError(err)
	}
	if status == http.StatusNotFound {
		return ports.ErrGPUSpecNotFound
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%w: delete gpu spec HTTP %d: %s", ports.ErrGPUSpecNotFound, status, string(respBody))
	}
	return nil
}

// getCRDByName fetches a GPUSpec CRD by its metadata.name.
func (s *CRDGPUSpecStore) getCRDByName(ctx context.Context, name string) (gpuSpecCRD, error) {
	endpoint := s.resourceURL(name)
	body, status, err := s.doer.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return gpuSpecCRD{}, mapGPUSpecK8sError(err)
	}
	if status == http.StatusNotFound {
		return gpuSpecCRD{}, ports.ErrGPUSpecNotFound
	}
	if status < 200 || status >= 300 {
		return gpuSpecCRD{}, fmt.Errorf("%w: get gpu spec HTTP %d", ports.ErrGPUSpecNotFound, status)
	}
	var crd gpuSpecCRD
	if err := json.Unmarshal(body, &crd); err != nil {
		return gpuSpecCRD{}, fmt.Errorf("%w: decode gpu spec: %v", ports.ErrGPUSpecNotFound, err)
	}
	return crd, nil
}

// findByLabel queries the GPUSpec CRD list with a label selector and
// returns the first matching CRD. Used for idempotency replay.
func (s *CRDGPUSpecStore) findByLabel(ctx context.Context, labelKey, labelValue string) (gpuSpecCRD, error) {
	selector := labelKey + "=" + labelValue
	endpoint := s.collectionURL(selector)
	body, _, err := s.doer.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return gpuSpecCRD{}, mapGPUSpecK8sError(err)
	}
	var list gpuSpecListCRD
	if err := json.Unmarshal(body, &list); err != nil {
		return gpuSpecCRD{}, fmt.Errorf("%w: decode gpu spec list: %v", ports.ErrGPUSpecNotFound, err)
	}
	if len(list.Items) == 0 {
		return gpuSpecCRD{}, ports.ErrGPUSpecNotFound
	}
	return list.Items[0], nil
}

func (s *CRDGPUSpecStore) collectionURL(labelSelector string) string {
	// GPUSpec CRD is cluster-scoped (scope=Cluster), so the REST path
	// omits the namespaces segment: /apis/{group}/{version}/{resource}
	base := fmt.Sprintf("%s/apis/%s/%s/%s",
		s.baseURL, gpuSpecAPIGroup, gpuSpecAPIVersion, gpuSpecResource)
	if labelSelector != "" {
		base += "?labelSelector=" + url.QueryEscape(labelSelector)
	}
	return base
}

func (s *CRDGPUSpecStore) resourceURL(name string) string {
	return fmt.Sprintf("%s/apis/%s/%s/%s/%s",
		s.baseURL, gpuSpecAPIGroup, gpuSpecAPIVersion, gpuSpecResource, name)
}

// crdToGPUSpec converts a GPUSpec CRD to the ports.GPUSpecCRD domain type.
// The spec ID is the CRD metadata.name (spec_id doubles as the public id).
func crdToGPUSpec(crd gpuSpecCRD) ports.GPUSpecCRD {
	spec := crd.Spec
	available := true
	if spec.Available != nil {
		available = *spec.Available
	}
	name := spec.Name
	if name == "" {
		// Derive display name from spec_id when not set.
		name = crd.Metadata.Name
	}
	return ports.GPUSpecCRD{
		ID:              crd.Metadata.Name,
		Name:            name,
		GPUType:         spec.GPUType,
		GPUMode:         spec.GPUMode,
		MemoryTotalMB:   spec.MemoryTotalMB,
		Shares:          spec.Shares,
		MBPerShare:      spec.MBPerShare,
		Available:       available,
		ComputePerShare: spec.ComputePerShare,
		NodeAffinity: ports.GPUSpecNodeAffinity{
			GPUSpec:          spec.NodeAffinity.GPUSpec,
			GPUSharingSpec:   spec.NodeAffinity.GPUSharingSpec,
			GPUSharingPolicy: spec.NodeAffinity.GPUSharingPolicy,
			GPUMode:          spec.NodeAffinity.GPUMode,
		},
		VolcanoResources: ports.GPUSpecVolcanoResources{
			Wholecard: spec.VolcanoResources.Wholecard,
			VGPU:      spec.VolcanoResources.VGPU,
		},
	}
}

// gpuSpecToCRD converts the ports.GPUSpecCRD domain type to a GPUSpec CRD.
func gpuSpecToCRD(spec ports.GPUSpecCRD) gpuSpecCRD {
	available := spec.Available
	return gpuSpecCRD{
		APIVersion: gpuSpecAPIGroup + "/" + gpuSpecAPIVersion,
		Kind:       gpuSpecKind,
		Metadata: gpuSpecCRDMeta{
			Name: spec.ID,
		},
		Spec: gpuSpecCRDSpec{
			Name:            spec.Name,
			GPUType:         spec.GPUType,
			GPUMode:         spec.GPUMode,
			MemoryTotalMB:   spec.MemoryTotalMB,
			Shares:          spec.Shares,
			MBPerShare:      spec.MBPerShare,
			Available:       &available,
			ComputePerShare: spec.ComputePerShare,
			NodeAffinity: gpuSpecCRDNodeAffinity{
				GPUSpec:          spec.NodeAffinity.GPUSpec,
				GPUSharingSpec:   spec.NodeAffinity.GPUSharingSpec,
				GPUSharingPolicy: spec.NodeAffinity.GPUSharingPolicy,
				GPUMode:          spec.NodeAffinity.GPUMode,
			},
			VolcanoResources: gpuSpecCRDVolcanoResources{
				Wholecard: spec.VolcanoResources.Wholecard,
				VGPU:      spec.VolcanoResources.VGPU,
			},
		},
	}
}

// validateGPUSpecID enforces K8s resource name convention:
// ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$, 1-63 characters (SPEC §5.2).
func validateGPUSpecID(specID string) error {
	specID = strings.TrimSpace(specID)
	if len(specID) == 0 || len(specID) > 63 {
		return fmt.Errorf("%w: spec_id must be 1-63 characters", ports.ErrGPUSpecNotFound)
	}
	for i, r := range specID {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '-' && i != 0 && i != len(specID)-1 {
			continue
		}
		return fmt.Errorf("%w: spec_id must match ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", ports.ErrGPUSpecNotFound)
	}
	return nil
}

// mapGPUSpecK8sError translates K8s REST errors into ports errors.
func mapGPUSpecK8sError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "404") || strings.Contains(msg, "not found") {
		return ports.ErrGPUSpecNotFound
	}
	if strings.Contains(msg, "409") || strings.Contains(msg, "conflict") {
		return ports.ErrGPUSpecConflict
	}
	return fmt.Errorf("%w: %v", ports.ErrGPUSpecNotFound, err)
}

var _ ports.GPUSpecStore = (*CRDGPUSpecStore)(nil)
