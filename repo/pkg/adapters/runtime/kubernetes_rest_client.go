package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const kubernetesApplyPatchContentType = "application/apply-patch+yaml"

type KubernetesRESTClientConfig struct {
	Host         string
	BearerToken  string
	FieldManager string
	HTTPClient   *http.Client
	Now          func() time.Time
}

type KubernetesRESTClient struct {
	host         string
	bearerToken  string
	fieldManager string
	httpClient   *http.Client
	now          func() time.Time
}

func NewKubernetesRESTClient(config KubernetesRESTClientConfig) (*KubernetesRESTClient, error) {
	host := strings.TrimRight(strings.TrimSpace(config.Host), "/")
	if host == "" {
		return nil, fmt.Errorf("%w: Kubernetes API host is required", ports.ErrInvalid)
	}
	if _, err := url.ParseRequestURI(host); err != nil {
		return nil, fmt.Errorf("%w: invalid Kubernetes API host: %v", ports.ErrInvalid, err)
	}

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	fieldManager := strings.TrimSpace(config.FieldManager)
	if fieldManager == "" {
		fieldManager = "ani-workload-runtime"
	}

	return &KubernetesRESTClient{
		host:         host,
		bearerToken:  strings.TrimSpace(config.BearerToken),
		fieldManager: fieldManager,
		httpClient:   client,
		now:          now,
	}, nil
}

func (c *KubernetesRESTClient) ServerSideDryRun(ctx context.Context, manifests []ports.WorkloadManifest) (ports.WorkloadProviderDryRunResult, error) {
	if len(manifests) == 0 {
		return ports.WorkloadProviderDryRunResult{}, fmt.Errorf("%w: at least one manifest is required for Kubernetes server-side dry-run", ports.ErrInvalid)
	}
	provider := manifests[0].Provider
	for _, manifest := range manifests {
		resource, err := parseKubernetesResource(manifest)
		if err != nil {
			return ports.WorkloadProviderDryRunResult{}, err
		}
		endpoint := c.collectionURL(resource, "dryRun=All")
		if _, err := c.do(ctx, http.MethodPost, endpoint, "application/json", []byte(manifest.Content)); err != nil {
			return ports.WorkloadProviderDryRunResult{}, err
		}
	}
	return ports.WorkloadProviderDryRunResult{
		Accepted:      true,
		Provider:      provider,
		ManifestCount: len(manifests),
		Reason:        "accepted by Kubernetes server-side dry-run dryRun=All",
		CheckedAt:     c.now().UTC(),
	}, nil
}

func (c *KubernetesRESTClient) Apply(ctx context.Context, request ports.WorkloadProviderApplyRequest) (ports.WorkloadProviderApplyResult, error) {
	if err := validateProviderApplyRequest(request); err != nil {
		return ports.WorkloadProviderApplyResult{}, err
	}
	provider := request.Manifests[0].Provider
	refs, err := c.ApplyManifests(ctx, request.Manifests)
	if err != nil {
		return ports.WorkloadProviderApplyResult{}, err
	}
	return ports.WorkloadProviderApplyResult{
		Applied:       true,
		Provider:      provider,
		ManifestCount: len(request.Manifests),
		Operation:     request.Operation,
		ResourceRefs:  refs,
		Reason:        "applied by Kubernetes REST client",
		Warnings:      request.DryRunResult.Warnings,
		AppliedAt:     c.now().UTC(),
	}, nil
}

func (c *KubernetesRESTClient) ApplyManifests(ctx context.Context, manifests []ports.WorkloadManifest) ([]string, error) {
	if len(manifests) == 0 {
		return nil, fmt.Errorf("%w: at least one manifest is required for Kubernetes apply", ports.ErrInvalid)
	}
	refs := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		resource, err := parseKubernetesResource(manifest)
		if err != nil {
			return nil, err
		}
		query := "fieldManager=" + url.QueryEscape(c.fieldManager) + "&force=true"
		if _, err := c.do(ctx, http.MethodPatch, c.resourceURL(resource, query), kubernetesApplyPatchContentType, []byte(manifest.Content)); err != nil {
			return nil, err
		}
		refs = append(refs, resource.ref())
	}
	return refs, nil
}

func (c *KubernetesRESTClient) ObserveNetworkResource(ctx context.Context, request ports.NetworkProviderStatusRequest) (ports.NetworkProviderStatusResult, error) {
	if request.TenantID == "" || request.ResourceKind == "" || request.ResourceID == "" {
		return ports.NetworkProviderStatusResult{}, fmt.Errorf("%w: tenant id, resource kind, and resource id are required for network observation", ports.ErrInvalid)
	}
	if !request.ApplyResult.Applied {
		return ports.NetworkProviderStatusResult{}, fmt.Errorf("%w: network provider apply must be applied before Kubernetes observation", ports.ErrInvalid)
	}
	if len(request.ApplyResult.ResourceRefs) == 0 {
		return ports.NetworkProviderStatusResult{}, fmt.Errorf("%w: network resource refs are required for Kubernetes observation", ports.ErrInvalid)
	}

	resource, err := resourceFromRef(request.ApplyResult.Provider, tenantNamespace(request.TenantID), request.ApplyResult.ResourceRefs[0])
	if err != nil {
		return ports.NetworkProviderStatusResult{}, err
	}
	body, err := c.do(ctx, http.MethodGet, c.resourceURL(resource, ""), "", nil)
	if err != nil {
		return ports.NetworkProviderStatusResult{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return ports.NetworkProviderStatusResult{}, fmt.Errorf("%w: invalid Kubernetes network observation response: %v", ports.ErrInvalid, err)
	}

	return ports.NetworkProviderStatusResult{
		TenantID:     request.TenantID,
		ResourceKind: request.ResourceKind,
		ResourceID:   request.ResourceID,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: append([]string(nil), request.ApplyResult.ResourceRefs...),
		State:        networkStateFromKubernetesObject(doc),
		Reason:       networkReasonFromKubernetesObject(doc),
		ObservedAt:   c.now().UTC(),
	}, nil
}

func (c *KubernetesRESTClient) ObserveStorageResource(ctx context.Context, request ports.StorageProviderStatusRequest) (ports.StorageProviderStatusResult, error) {
	if request.TenantID == "" || request.ResourceKind == "" || request.ResourceID == "" {
		return ports.StorageProviderStatusResult{}, fmt.Errorf("%w: tenant id, resource kind, and resource id are required for storage observation", ports.ErrInvalid)
	}
	if !request.ApplyResult.Applied {
		return ports.StorageProviderStatusResult{}, fmt.Errorf("%w: storage provider apply must be applied before Kubernetes observation", ports.ErrInvalid)
	}
	if len(request.ApplyResult.ResourceRefs) == 0 {
		return ports.StorageProviderStatusResult{}, fmt.Errorf("%w: storage resource refs are required for Kubernetes observation", ports.ErrInvalid)
	}

	resource, err := resourceFromRef(request.ApplyResult.Provider, tenantNamespace(request.TenantID), request.ApplyResult.ResourceRefs[0])
	if err != nil {
		return ports.StorageProviderStatusResult{}, err
	}
	body, err := c.do(ctx, http.MethodGet, c.resourceURL(resource, ""), "", nil)
	if err != nil {
		return ports.StorageProviderStatusResult{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return ports.StorageProviderStatusResult{}, fmt.Errorf("%w: invalid Kubernetes storage observation response: %v", ports.ErrInvalid, err)
	}

	return ports.StorageProviderStatusResult{
		TenantID:     request.TenantID,
		ResourceKind: request.ResourceKind,
		ResourceID:   request.ResourceID,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: append([]string(nil), request.ApplyResult.ResourceRefs...),
		State:        storageStateFromKubernetesObject(doc),
		Reason:       storageReasonFromKubernetesObject(doc),
		ObservedAt:   c.now().UTC(),
	}, nil
}

func (c *KubernetesRESTClient) Observe(ctx context.Context, request ports.WorkloadProviderStatusRequest) (ports.WorkloadProviderObservation, error) {
	if request.TenantID == "" || request.InstanceID == "" || request.Kind == "" {
		return ports.WorkloadProviderObservation{}, fmt.Errorf("%w: tenant id, instance id, and workload kind are required for Kubernetes observation", ports.ErrInvalid)
	}
	if !request.ApplyResult.Applied {
		return ports.WorkloadProviderObservation{}, fmt.Errorf("%w: provider apply must be applied before Kubernetes observation", ports.ErrInvalid)
	}
	if len(request.ApplyResult.ResourceRefs) == 0 {
		return ports.WorkloadProviderObservation{}, fmt.Errorf("%w: resource refs are required for Kubernetes observation", ports.ErrInvalid)
	}

	resource, err := resourceFromRef(request.ApplyResult.Provider, tenantNamespace(request.TenantID), request.ApplyResult.ResourceRefs[0])
	if err != nil {
		return ports.WorkloadProviderObservation{}, err
	}
	body, err := c.do(ctx, http.MethodGet, c.resourceURL(resource, ""), "", nil)
	if err != nil {
		return ports.WorkloadProviderObservation{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return ports.WorkloadProviderObservation{}, fmt.Errorf("%w: invalid Kubernetes observation response: %v", ports.ErrInvalid, err)
	}

	return ports.WorkloadProviderObservation{
		TenantID:     request.TenantID,
		InstanceID:   request.InstanceID,
		Kind:         request.Kind,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: request.ApplyResult.ResourceRefs,
		Phase:        phaseFromKubernetesObject(resource, doc),
		NodeName:     nodeNameFromKubernetesObject(doc),
		Reason:       reasonFromKubernetesObject(doc),
		ObservedAt:   c.now().UTC(),
	}, nil
}

func (c *KubernetesRESTClient) do(ctx context.Context, method string, endpoint string, contentType string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: Kubernetes API %s %s returned %d: %s", ports.ErrInvalid, method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (c *KubernetesRESTClient) collectionURL(resource kubernetesResource, query string) string {
	return c.host + resource.collectionPath() + querySuffix(query)
}

func (c *KubernetesRESTClient) resourceURL(resource kubernetesResource, query string) string {
	return c.host + resource.resourcePath() + querySuffix(query)
}

type kubernetesResource struct {
	Provider   string
	APIGroup   string
	APIVersion string
	Resource   string
	Kind       string
	Namespace  string
	Name       string
	Namespaced bool
}

func (r kubernetesResource) collectionPath() string {
	if !r.Namespaced {
		if r.APIGroup == "" {
			return "/api/" + r.APIVersion + "/" + r.Resource
		}
		return "/apis/" + r.APIGroup + "/" + r.APIVersion + "/" + r.Resource
	}
	if r.APIGroup == "" {
		return "/api/" + r.APIVersion + "/namespaces/" + url.PathEscape(r.Namespace) + "/" + r.Resource
	}
	return "/apis/" + r.APIGroup + "/" + r.APIVersion + "/namespaces/" + url.PathEscape(r.Namespace) + "/" + r.Resource
}

func (r kubernetesResource) resourcePath() string {
	return r.collectionPath() + "/" + url.PathEscape(r.Name)
}

func (r kubernetesResource) ref() string {
	return r.Provider + "/" + r.Kind + "/" + r.Name
}

func parseKubernetesResource(manifest ports.WorkloadManifest) (kubernetesResource, error) {
	doc, err := parseManifestDocument(manifest.Content)
	if err != nil {
		return kubernetesResource{}, err
	}
	apiVersion, _ := doc["apiVersion"].(string)
	kind, _ := doc["kind"].(string)
	metadata, _ := doc["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	if name == "" {
		return kubernetesResource{}, fmt.Errorf("%w: Kubernetes manifest metadata.name is required", ports.ErrInvalid)
	}
	resource, err := resourceMapping(manifest.Provider, apiVersion, kind)
	if err != nil {
		return kubernetesResource{}, err
	}
	if resource.Namespaced && namespace == "" {
		return kubernetesResource{}, fmt.Errorf("%w: Kubernetes manifest metadata.namespace is required for %s", ports.ErrInvalid, kind)
	}
	resource.Namespace = namespace
	resource.Name = name
	return resource, nil
}

func resourceFromRef(provider string, namespace string, ref string) (kubernetesResource, error) {
	parts := strings.Split(ref, "/")
	if len(parts) != 3 {
		return kubernetesResource{}, fmt.Errorf("%w: Kubernetes resource ref must be provider/kind/name", ports.ErrInvalid)
	}
	refProvider, kind, name := parts[0], parts[1], parts[2]
	if provider != "" && provider != refProvider {
		return kubernetesResource{}, fmt.Errorf("%w: Kubernetes resource ref provider does not match apply result", ports.ErrInvalid)
	}
	resource, err := resourceMapping(refProvider, "", kind)
	if err != nil {
		return kubernetesResource{}, err
	}
	resource.Namespace = namespace
	resource.Name = name
	return resource, nil
}

func resourceMapping(provider string, apiVersion string, kind string) (kubernetesResource, error) {
	switch provider + "/" + kind {
	case "kubernetes/Deployment":
		return kubernetesResource{Provider: provider, APIGroup: "apps", APIVersion: "v1", Resource: "deployments", Kind: kind, Namespaced: true}, nil
	case "kubernetes/Job":
		return kubernetesResource{Provider: provider, APIGroup: "batch", APIVersion: "v1", Resource: "jobs", Kind: kind, Namespaced: true}, nil
	case "kubernetes/NetworkPolicy":
		return kubernetesResource{Provider: provider, APIGroup: "networking.k8s.io", APIVersion: "v1", Resource: "networkpolicies", Kind: kind, Namespaced: true}, nil
	case "kubernetes/Service":
		return kubernetesResource{Provider: provider, APIGroup: "", APIVersion: "v1", Resource: "services", Kind: kind, Namespaced: true}, nil
	case "kubernetes/PersistentVolumeClaim":
		return kubernetesResource{Provider: provider, APIGroup: "", APIVersion: "v1", Resource: "persistentvolumeclaims", Kind: kind, Namespaced: true}, nil
	case "kubernetes/Secret":
		return kubernetesResource{Provider: provider, APIGroup: "", APIVersion: "v1", Resource: "secrets", Kind: kind, Namespaced: true}, nil
	case "kubevirt/VirtualMachine":
		return kubernetesResource{Provider: provider, APIGroup: "kubevirt.io", APIVersion: "v1", Resource: "virtualmachines", Kind: kind, Namespaced: true}, nil
	case "clusterapi/MachineDeployment":
		return kubernetesResource{Provider: provider, APIGroup: "cluster.x-k8s.io", APIVersion: "v1beta1", Resource: "machinedeployments", Kind: kind, Namespaced: true}, nil
	case "kubeovn/Vpc":
		return kubernetesResource{Provider: provider, APIGroup: "kubeovn.io", APIVersion: "v1", Resource: "vpcs", Kind: kind}, nil
	case "kubeovn/Subnet":
		return kubernetesResource{Provider: provider, APIGroup: "kubeovn.io", APIVersion: "v1", Resource: "subnets", Kind: kind}, nil
	default:
		if apiVersion != "" {
			return kubernetesResource{}, fmt.Errorf("%w: unsupported Kubernetes provider resource %s %s/%s", ports.ErrUnsupported, provider, apiVersion, kind)
		}
		return kubernetesResource{}, fmt.Errorf("%w: unsupported Kubernetes provider resource %s/%s", ports.ErrUnsupported, provider, kind)
	}
}

func querySuffix(query string) string {
	if strings.TrimSpace(query) == "" {
		return ""
	}
	return "?" + query
}

func phaseFromKubernetesObject(resource kubernetesResource, doc map[string]any) string {
	status, _ := doc["status"].(map[string]any)
	switch resource.Kind {
	case "Deployment":
		if numericStatus(status, "availableReplicas") > 0 || numericStatus(status, "readyReplicas") > 0 {
			return "Running"
		}
		if numericStatus(status, "replicas") > 0 || numericStatus(status, "updatedReplicas") > 0 {
			return "Provisioning"
		}
	case "Job":
		if numericStatus(status, "failed") > 0 {
			return "Failed"
		}
		if numericStatus(status, "succeeded") > 0 {
			return "Succeeded"
		}
		if numericStatus(status, "active") > 0 {
			return "Running"
		}
	case "VirtualMachine":
		if phase, _ := status["printableStatus"].(string); phase != "" {
			return phase
		}
		if phase, _ := status["phase"].(string); phase != "" {
			return phase
		}
	}
	return "Pending"
}

func nodeNameFromKubernetesObject(doc map[string]any) string {
	status, _ := doc["status"].(map[string]any)
	if node, _ := status["nodeName"].(string); node != "" {
		return node
	}
	return ""
}

func reasonFromKubernetesObject(doc map[string]any) string {
	status, _ := doc["status"].(map[string]any)
	if reason, _ := status["reason"].(string); reason != "" {
		return reason
	}
	return ""
}

func networkStateFromKubernetesObject(doc map[string]any) ports.NetworkResourceState {
	metadata, _ := doc["metadata"].(map[string]any)
	if deletionTimestamp, _ := metadata["deletionTimestamp"].(string); deletionTimestamp != "" {
		return ports.NetworkResourceDeleting
	}
	status, _ := doc["status"].(map[string]any)
	if phase, _ := status["phase"].(string); strings.EqualFold(phase, "failed") {
		return ports.NetworkResourceFailed
	}
	for _, condition := range kubernetesConditions(status) {
		conditionType, _ := condition["type"].(string)
		conditionStatus, _ := condition["status"].(string)
		if (conditionType == "Ready" || conditionType == "Available") && conditionStatus == "False" {
			return ports.NetworkResourceFailed
		}
	}
	return ports.NetworkResourceAvailable
}

func networkReasonFromKubernetesObject(doc map[string]any) string {
	status, _ := doc["status"].(map[string]any)
	for _, condition := range kubernetesConditions(status) {
		conditionType, _ := condition["type"].(string)
		conditionStatus, _ := condition["status"].(string)
		if (conditionType == "Ready" || conditionType == "Available") && conditionStatus == "False" {
			if message, _ := condition["message"].(string); message != "" {
				return message
			}
			if reason, _ := condition["reason"].(string); reason != "" {
				return reason
			}
		}
	}
	if reason, _ := status["reason"].(string); reason != "" {
		return reason
	}
	return "observed by Kubernetes network provider"
}

func storageStateFromKubernetesObject(doc map[string]any) ports.StorageResourceState {
	metadata, _ := doc["metadata"].(map[string]any)
	if deletionTimestamp, _ := metadata["deletionTimestamp"].(string); deletionTimestamp != "" {
		return ports.StorageResourceDeleting
	}
	status, _ := doc["status"].(map[string]any)
	if phase, _ := status["phase"].(string); phase != "" {
		switch strings.ToLower(phase) {
		case "bound", "available":
			return ports.StorageResourceAvailable
		case "lost", "failed":
			return ports.StorageResourceFailed
		case "pending":
			return ports.StorageResourcePending
		}
	}
	for _, condition := range kubernetesConditions(status) {
		conditionType, _ := condition["type"].(string)
		conditionStatus, _ := condition["status"].(string)
		if conditionType == "FileSystemResizePending" && conditionStatus == "True" {
			return ports.StorageResourcePending
		}
	}
	return ports.StorageResourceAvailable
}

func storageReasonFromKubernetesObject(doc map[string]any) string {
	status, _ := doc["status"].(map[string]any)
	for _, condition := range kubernetesConditions(status) {
		if message, _ := condition["message"].(string); message != "" {
			return message
		}
		if reason, _ := condition["reason"].(string); reason != "" {
			return reason
		}
	}
	if reason, _ := status["reason"].(string); reason != "" {
		return reason
	}
	if phase, _ := status["phase"].(string); phase != "" {
		return "observed Kubernetes PVC phase " + phase
	}
	return "observed by Kubernetes storage provider"
}

func kubernetesConditions(status map[string]any) []map[string]any {
	rawConditions, _ := status["conditions"].([]any)
	conditions := make([]map[string]any, 0, len(rawConditions))
	for _, rawCondition := range rawConditions {
		if condition, ok := rawCondition.(map[string]any); ok {
			conditions = append(conditions, condition)
		}
	}
	return conditions
}

func numericStatus(status map[string]any, key string) int64 {
	switch value := status[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

var _ KubernetesProviderClient = (*KubernetesRESTClient)(nil)
