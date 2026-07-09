package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
)

// CDIRESTClient performs raw JSON REST calls against the Kubernetes API server
// for CDI DataVolume / UploadTokenRequest custom resources. It intentionally
// exposes a small, generic surface so Gateway handlers never need to assemble
// CDI YAML/JSON themselves; only this adapter package does.
type CDIRESTClient interface {
	CreateNamespacedResource(ctx context.Context, apiGroupVersion, resource, namespace string, body map[string]any) (map[string]any, error)
	GetNamespacedResource(ctx context.Context, apiGroupVersion, resource, namespace, name string) (map[string]any, error)
	ListNamespacedResource(ctx context.Context, apiGroupVersion, resource, namespace, labelSelector string) ([]map[string]any, error)
	DeleteNamespacedResource(ctx context.Context, apiGroupVersion, resource, namespace, name string) error
}

const (
	cdiAPIGroupVersion       = "cdi.kubevirt.io/v1beta1"
	cdiUploadAPIGroupVersion = "upload.cdi.kubevirt.io/v1beta1"
	cdiDataVolumesResource   = "datavolumes"
	cdiUploadTokensResource  = "uploadtokenrequests"
	defaultCDIStorageClass   = "ani-rbd-ssd"
	defaultCDIUploadPath     = "/v1beta1/upload"
	cdiUploadSessionTTL      = 15 * time.Minute

	cdiImmediateBindAnnotation  = "cdi.kubevirt.io/storage.bind.immediate.requested"
	cdiImageNameAnnotation      = "ani.io/image-name"
	cdiIdempotencyKeyAnnotation = "ani.io/idempotency-key"
	cdiContentTypeAnnotation    = "ani.io/content-type"
	cdiManagedByLabel           = "app.kubernetes.io/managed-by"
	cdiManagedByLabelValue      = "ani-image-import"
)

// CDIImageImportService implements ports.ImageImportService against a real
// CDI/KubeVirt cluster. DataVolume is the source of truth for image state;
// no separate database table is introduced.
type CDIImageImportService struct {
	client        CDIRESTClient
	uploadBaseURL string
	storageClass  string
	now           func() time.Time
}

type CDIImageImportOption func(*CDIImageImportService)

func WithCDIStorageClass(storageClass string) CDIImageImportOption {
	return func(s *CDIImageImportService) {
		if trimmed := strings.TrimSpace(storageClass); trimmed != "" {
			s.storageClass = trimmed
		}
	}
}

func WithCDIImageImportClock(now func() time.Time) CDIImageImportOption {
	return func(s *CDIImageImportService) {
		if now != nil {
			s.now = now
		}
	}
}

func NewCDIImageImportService(client CDIRESTClient, uploadBaseURL string, options ...CDIImageImportOption) *CDIImageImportService {
	svc := &CDIImageImportService{
		client:        client,
		uploadBaseURL: strings.TrimRight(strings.TrimSpace(uploadBaseURL), "/"),
		storageClass:  defaultCDIStorageClass,
		now:           func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(svc)
	}
	return svc
}

func (s *CDIImageImportService) CreateUpload(ctx context.Context, req ports.ImageUploadCreateRequest) (ports.ImageUploadSession, error) {
	if err := validateImageUploadCreateRequest(req); err != nil {
		return ports.ImageUploadSession{}, err
	}
	if req.Format != ports.ImageFormatISO {
		return ports.ImageUploadSession{}, fmt.Errorf("%w: image format %q", ports.ErrUnsupported, req.Format)
	}

	namespace := tenantNamespace(req.TenantID)
	name := cdiImageResourceName(req.TenantID, req.IdempotencyKey)
	now := s.now().UTC()

	if existing, err := s.client.GetNamespacedResource(ctx, cdiAPIGroupVersion, cdiDataVolumesResource, namespace, name); err == nil {
		record := imageRecordFromDataVolume(req.TenantID, existing)
		return s.mintUploadSession(ctx, namespace, name, record, now)
	} else if !isCDINotFound(err) {
		return ports.ImageUploadSession{}, err
	}

	storageClass := strings.TrimSpace(req.StorageClass)
	if storageClass == "" {
		storageClass = s.storageClass
	}
	dv := cdiDataVolumeManifest(namespace, name, req, storageClass)
	created, err := s.client.CreateNamespacedResource(ctx, cdiAPIGroupVersion, cdiDataVolumesResource, namespace, dv)
	if err != nil {
		return ports.ImageUploadSession{}, err
	}
	record := imageRecordFromDataVolume(req.TenantID, created)
	return s.mintUploadSession(ctx, namespace, name, record, now)
}

func (s *CDIImageImportService) mintUploadSession(ctx context.Context, namespace, name string, record ports.ImageRecord, now time.Time) (ports.ImageUploadSession, error) {
	utr := cdiUploadTokenRequestManifest(namespace, name)
	created, err := s.client.CreateNamespacedResource(ctx, cdiUploadAPIGroupVersion, cdiUploadTokensResource, namespace, utr)
	if err != nil {
		return ports.ImageUploadSession{}, err
	}
	token, _ := nestedString(created, "status", "token")
	if token == "" {
		return ports.ImageUploadSession{}, fmt.Errorf("%w: CDI UploadTokenRequest did not return a token", ports.ErrInvalid)
	}
	return ports.ImageUploadSession{
		Image:     record,
		UploadURL: s.uploadBaseURL + defaultCDIUploadPath,
		Token:     token,
		ExpiresAt: now.Add(cdiUploadSessionTTL),
		Method:    "POST",
	}, nil
}

func (s *CDIImageImportService) Get(ctx context.Context, req ports.ImageGetRequest) (ports.ImageRecord, error) {
	namespace := tenantNamespace(req.TenantID)
	doc, err := s.client.GetNamespacedResource(ctx, cdiAPIGroupVersion, cdiDataVolumesResource, namespace, req.ImageID)
	if err != nil {
		return ports.ImageRecord{}, err
	}
	return imageRecordFromDataVolume(req.TenantID, doc), nil
}

func (s *CDIImageImportService) List(ctx context.Context, req ports.ImageListRequest) (ports.ImageListResult, error) {
	namespace := tenantNamespace(req.TenantID)
	docs, err := s.client.ListNamespacedResource(ctx, cdiAPIGroupVersion, cdiDataVolumesResource, namespace, cdiManagedByLabel+"="+cdiManagedByLabelValue)
	if err != nil {
		return ports.ImageListResult{}, err
	}
	items := make([]ports.ImageRecord, 0, len(docs))
	for _, doc := range docs {
		record := imageRecordFromDataVolume(req.TenantID, doc)
		if req.Format != "" && record.Format != req.Format {
			continue
		}
		if req.State != "" && record.State != req.State {
			continue
		}
		items = append(items, record)
	}
	sortImageRecordsByCreatedAt(items)
	return ports.ImageListResult{Items: items, Total: len(items)}, nil
}

func (s *CDIImageImportService) Delete(ctx context.Context, req ports.ImageDeleteRequest) (ports.ImageRecord, error) {
	namespace := tenantNamespace(req.TenantID)
	doc, err := s.client.GetNamespacedResource(ctx, cdiAPIGroupVersion, cdiDataVolumesResource, namespace, req.ImageID)
	if err != nil {
		return ports.ImageRecord{}, err
	}
	record := imageRecordFromDataVolume(req.TenantID, doc)
	if err := s.client.DeleteNamespacedResource(ctx, cdiAPIGroupVersion, cdiDataVolumesResource, namespace, req.ImageID); err != nil {
		return ports.ImageRecord{}, err
	}
	record.State = ports.ImageStateDeleting
	record.UpdatedAt = s.now().UTC()
	return record, nil
}

func cdiImageResourceName(tenantID, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "/" + strings.TrimSpace(idempotencyKey)))
	return "img-" + hex.EncodeToString(digest[:])[:20]
}

func cdiDataVolumeManifest(namespace, name string, req ports.ImageUploadCreateRequest, storageClass string) map[string]any {
	annotations := map[string]any{
		cdiImmediateBindAnnotation:  "true",
		cdiImageNameAnnotation:      req.Name,
		cdiIdempotencyKeyAnnotation: req.IdempotencyKey,
	}
	if contentType := strings.TrimSpace(req.ContentType); contentType != "" {
		annotations[cdiContentTypeAnnotation] = contentType
	}
	return map[string]any{
		"apiVersion": cdiAPIGroupVersion,
		"kind":       "DataVolume",
		"metadata": map[string]any{
			"name":        name,
			"namespace":   namespace,
			"annotations": annotations,
			"labels": map[string]any{
				cdiManagedByLabel: cdiManagedByLabelValue,
			},
		},
		"spec": map[string]any{
			"source": map[string]any{"upload": map[string]any{}},
			"storage": map[string]any{
				"resources":        map[string]any{"requests": map[string]any{"storage": strconv.FormatInt(req.SizeGiB, 10) + "Gi"}},
				"storageClassName": storageClass,
			},
		},
	}
}

func cdiUploadTokenRequestManifest(namespace, dataVolumeName string) map[string]any {
	return map[string]any{
		"apiVersion": cdiUploadAPIGroupVersion,
		"kind":       "UploadTokenRequest",
		"metadata": map[string]any{
			"name":      dataVolumeName + "-" + uuid.NewString()[:8],
			"namespace": namespace,
		},
		"spec": map[string]any{
			"pvcName": dataVolumeName,
		},
	}
}

func imageRecordFromDataVolume(tenantID string, doc map[string]any) ports.ImageRecord {
	metadata, _ := doc["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	annotations, _ := metadata["annotations"].(map[string]any)
	imageName, _ := annotations[cdiImageNameAnnotation].(string)
	contentType, _ := annotations[cdiContentTypeAnnotation].(string)

	spec, _ := doc["spec"].(map[string]any)
	storage, _ := spec["storage"].(map[string]any)
	storageClassName, _ := storage["storageClassName"].(string)

	status, _ := doc["status"].(map[string]any)
	phase, _ := status["phase"].(string)
	state, reason, message := cdiImageStateFromDataVolumePhase(phase, status)

	createdAt := cdiParseTimestamp(metadata["creationTimestamp"])
	updatedAt := createdAt
	if latest := cdiLatestConditionTime(status); latest.After(updatedAt) {
		updatedAt = latest
	}

	return ports.ImageRecord{
		ID:           name,
		TenantID:     tenantID,
		Name:         firstNonEmpty(imageName, name),
		Format:       ports.ImageFormatISO,
		SizeGiB:      cdiStorageRequestGiB(storage),
		ContentType:  contentType,
		State:        state,
		Reason:       reason,
		Message:      message,
		StorageClass: storageClassName,
		DevProfile:   cdiImageDevProfile(),
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
}

func cdiImageStateFromDataVolumePhase(phase string, status map[string]any) (ports.ImageState, string, string) {
	reason, message := cdiConditionReasonMessage(status)
	switch phase {
	case "", "Pending", "WaitForFirstConsumer":
		return ports.ImageStatePending, reason, message
	case "UploadScheduled", "UploadReady":
		return ports.ImageStateUploading, reason, message
	case "ImportScheduled", "ImportInProgress", "CloneScheduled", "CloneInProgress":
		return ports.ImageStateProcessing, reason, message
	case "Succeeded":
		return ports.ImageStateReady, reason, message
	case "Failed":
		return ports.ImageStateFailed, reason, message
	default:
		return ports.ImageStateProcessing, reason, message
	}
}

func cdiImageDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "real",
		Provider:     "cdi-datavolume-provider",
		RealProvider: true,
		Reason:       "backed by CDI DataVolume/UploadTokenRequest; client is responsible for uploadproxy TLS trust",
	}
}

func cdiStorageRequestGiB(storage map[string]any) int64 {
	resources, _ := storage["resources"].(map[string]any)
	requests, _ := resources["requests"].(map[string]any)
	raw, _ := requests["storage"].(string)
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "Gi")
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func cdiParseTimestamp(value any) time.Time {
	s, _ := value.(string)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func cdiLatestConditionTime(status map[string]any) time.Time {
	var latest time.Time
	for _, condition := range kubernetesConditions(status) {
		if t := cdiParseTimestamp(condition["lastTransitionTime"]); t.After(latest) {
			latest = t
		}
	}
	return latest
}

func cdiConditionReasonMessage(status map[string]any) (string, string) {
	for _, condition := range kubernetesConditions(status) {
		if message, _ := condition["message"].(string); message != "" {
			reason, _ := condition["reason"].(string)
			return reason, message
		}
	}
	return "", ""
}

func sortImageRecordsByCreatedAt(items []ports.ImageRecord) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].CreatedAt.Before(items[j-1].CreatedAt); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func nestedString(doc map[string]any, path ...string) (string, bool) {
	var current any = doc
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = m[key]
		if !ok {
			return "", false
		}
	}
	s, ok := current.(string)
	return s, ok
}

func isCDINotFound(err error) bool {
	return errors.Is(err, ports.ErrNotFound)
}

// cdiKubernetesRESTClient is the real CDIRESTClient implementation, talking
// directly to the Kubernetes API server for CDI/upload custom resources.
type cdiKubernetesRESTClient struct {
	host        string
	bearerToken string
	httpClient  *http.Client
	policy      resilience.Policy
}

// NewCDIKubernetesRESTClient resolves Kubernetes REST credentials the same
// way NewKubernetesRESTClient does (explicit env > kubeconfig > in-cluster)
// and returns a CDIRESTClient bound to that API server.
func NewCDIKubernetesRESTClient(config KubernetesRESTClientConfig) (CDIRESTClient, error) {
	resolved, _, err := ResolveKubernetesRESTClientConfig(config)
	if err != nil {
		return nil, err
	}
	config = resolved

	host, inCluster, err := kubernetesRESTHost(config)
	if err != nil {
		return nil, err
	}
	if _, err := url.ParseRequestURI(host); err != nil {
		return nil, fmt.Errorf("%w: invalid Kubernetes API host: %v", ports.ErrInvalid, err)
	}

	bearerToken := strings.TrimSpace(config.BearerToken)
	if bearerToken == "" && inCluster {
		bearerToken, err = readKubernetesServiceAccountToken(config.BearerTokenFile)
		if err != nil {
			return nil, err
		}
	}
	client := config.HTTPClient
	if client == nil {
		client, err = kubernetesHTTPClient(config.CAFile, inCluster)
		if err != nil {
			return nil, err
		}
	}

	return &cdiKubernetesRESTClient{
		host:        host,
		bearerToken: bearerToken,
		httpClient:  client,
		policy:      resilience.Policy{Timeout: config.RequestTimeout},
	}, nil
}

func (c *cdiKubernetesRESTClient) CreateNamespacedResource(ctx context.Context, apiGroupVersion, resource, namespace string, body map[string]any) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ports.ErrInvalid, err)
	}
	return c.do(ctx, http.MethodPost, c.collectionURL(apiGroupVersion, resource, namespace), data)
}

func (c *cdiKubernetesRESTClient) GetNamespacedResource(ctx context.Context, apiGroupVersion, resource, namespace, name string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, c.resourceURL(apiGroupVersion, resource, namespace, name), nil)
}

func (c *cdiKubernetesRESTClient) ListNamespacedResource(ctx context.Context, apiGroupVersion, resource, namespace, labelSelector string) ([]map[string]any, error) {
	endpoint := c.collectionURL(apiGroupVersion, resource, namespace)
	if trimmed := strings.TrimSpace(labelSelector); trimmed != "" {
		endpoint += "?labelSelector=" + url.QueryEscape(trimmed)
	}
	doc, err := c.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	rawItems, _ := doc["items"].([]any)
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func (c *cdiKubernetesRESTClient) DeleteNamespacedResource(ctx context.Context, apiGroupVersion, resource, namespace, name string) error {
	_, err := c.do(ctx, http.MethodDelete, c.resourceURL(apiGroupVersion, resource, namespace, name), nil)
	return err
}

func (c *cdiKubernetesRESTClient) collectionURL(apiGroupVersion, resource, namespace string) string {
	return c.host + "/apis/" + apiGroupVersion + "/namespaces/" + url.PathEscape(namespace) + "/" + resource
}

func (c *cdiKubernetesRESTClient) resourceURL(apiGroupVersion, resource, namespace, name string) string {
	return c.collectionURL(apiGroupVersion, resource, namespace) + "/" + url.PathEscape(name)
}

func (c *cdiKubernetesRESTClient) do(ctx context.Context, method, endpoint string, body []byte) (map[string]any, error) {
	var result map[string]any
	err := resilience.Do(ctx, c.policy, func(callCtx context.Context) error {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(callCtx, method, endpoint, reader)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		if c.bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.bearerToken)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return readErr
		}
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return ports.ErrNotFound
		case resp.StatusCode == http.StatusConflict:
			return ports.ErrConflict
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			statusErr := resilience.NewStatusError("CDI", method, req.URL.Path, resp.StatusCode, string(data))
			if resilience.Retryable(statusErr) {
				return statusErr
			}
			return fmt.Errorf("%w: %v", ports.ErrInvalid, statusErr)
		}
		if len(data) == 0 {
			result = map[string]any{}
			return nil
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("%w: invalid CDI response: %v", ports.ErrInvalid, err)
		}
		return nil
	})
	return result, err
}

var _ CDIRESTClient = (*cdiKubernetesRESTClient)(nil)
