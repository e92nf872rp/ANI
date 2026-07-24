package vectorstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
)

const (
	defaultMilvusMetric = "COSINE"
)

var milvusCollectionSafePattern = regexp.MustCompile(`[^a-zA-Z0-9_]`)

type MilvusVectorStoreConfig struct {
	Endpoint         string
	Endpoints        []string
	Token            string
	Database         string
	CollectionPrefix string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
}

type MilvusVectorStore struct {
	endpoint         *url.URL
	endpoints        []*url.URL
	token            string
	database         string
	collectionPrefix string
	client           *http.Client
	policy           resilience.Policy
}

var _ ports.VectorStore = (*MilvusVectorStore)(nil)

func NewMilvusVectorStore(config MilvusVectorStoreConfig) (*MilvusVectorStore, error) {
	endpoints, err := parseMilvusEndpoints(config.Endpoint, config.Endpoints)
	if err != nil {
		return nil, err
	}
	endpoint := endpoints[0]
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &MilvusVectorStore{
		endpoint:         endpoint,
		endpoints:        endpoints,
		token:            strings.TrimSpace(config.Token),
		database:         strings.TrimSpace(config.Database),
		collectionPrefix: strings.TrimSpace(config.CollectionPrefix),
		client:           client,
		policy:           resilience.Policy{Timeout: config.RequestTimeout},
	}, nil
}

func (s *MilvusVectorStore) EnsureCollection(ctx context.Context, ref ports.VectorCollectionRef, dimension int) error {
	if dimension <= 0 {
		return fmt.Errorf("%w: vector collection dimension must be greater than zero", ports.ErrInvalid)
	}
	body := s.collectionPayload(ref)
	body["dimension"] = dimension
	body["metricType"] = defaultMilvusMetric
	body["primaryFieldName"] = "id"
	body["vectorFieldName"] = "vector"
	body["idType"] = "VarChar"
	body["params"] = map[string]string{"max_length": "256"}
	return s.doMilvus(ctx, "/v2/vectordb/collections/create", body, nil, milvusAllowAlreadyExists)
}

func (s *MilvusVectorStore) Upsert(ctx context.Context, ref ports.VectorCollectionRef, records []ports.VectorRecord) error {
	if len(records) == 0 {
		return fmt.Errorf("%w: vector records are required", ports.ErrInvalid)
	}
	data := make([]map[string]any, 0, len(records))
	for _, record := range records {
		id := strings.TrimSpace(record.ID)
		if id == "" {
			return fmt.Errorf("%w: vector record id is required", ports.ErrInvalid)
		}
		if len(record.Vector) == 0 {
			return fmt.Errorf("%w: vector record vector is required", ports.ErrInvalid)
		}
		item := map[string]any{
			"id":     id,
			"vector": append([]float32(nil), record.Vector...),
		}
		metadata := cloneMetadata(record.Metadata)
		if content := strings.TrimSpace(metadata["content"]); content != "" {
			item["content"] = content
			delete(metadata, "content")
		}
		if len(metadata) > 0 {
			item["metadata"] = metadata
		}
		data = append(data, item)
	}
	body := s.collectionPayload(ref)
	body["data"] = data
	return s.doMilvus(ctx, "/v2/vectordb/entities/upsert", body, nil, nil)
}

func (s *MilvusVectorStore) Search(ctx context.Context, query ports.VectorSearchQuery) ([]ports.VectorSearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("%w: search vector is required", ports.ErrInvalid)
	}
	limit := query.TopK
	if limit <= 0 {
		limit = 10
	}
	body := s.collectionPayload(query.Collection)
	body["data"] = [][]float32{append([]float32(nil), query.Vector...)}
	body["limit"] = limit
	body["outputFields"] = []string{"metadata", "content"}
	if filter := milvusFilter(query.Filter); filter != "" {
		body["filter"] = filter
	}
	var response milvusResponse
	if err := s.doMilvus(ctx, "/v2/vectordb/entities/search", body, &response, nil); err != nil {
		return nil, err
	}
	return milvusSearchResults(response.Data), nil
}

func (s *MilvusVectorStore) Delete(ctx context.Context, ref ports.VectorCollectionRef, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: vector ids are required", ports.ErrInvalid)
	}
	body := s.collectionPayload(ref)
	body["filter"] = milvusIDFilter(ids)
	return s.doMilvus(ctx, "/v2/vectordb/entities/delete", body, nil, nil)
}

func (s *MilvusVectorStore) DeleteByExpr(ctx context.Context, ref ports.VectorCollectionRef, expr string) (int, error) {
	if strings.TrimSpace(expr) == "" {
		return 0, fmt.Errorf("%w: filter expression is required", ports.ErrInvalid)
	}
	body := s.collectionPayload(ref)
	body["filter"] = expr
	var response milvusResponse
	if err := s.doMilvus(ctx, "/v2/vectordb/entities/delete", body, &response, nil); err != nil {
		return 0, err
	}
	return milvusDeleteCount(response.Data), nil
}

func (s *MilvusVectorStore) Health(ctx context.Context) error {
	body := map[string]any{}
	if s.database != "" {
		body["dbName"] = s.database
	}
	return s.doMilvus(ctx, "/v2/vectordb/collections/list", body, nil, nil)
}

func (s *MilvusVectorStore) CollectionHealth(ctx context.Context, ref ports.VectorCollectionRef) (ports.VectorCollectionHealth, error) {
	body := s.collectionPayload(ref)
	var response milvusResponse
	err := s.doMilvus(ctx, "/v2/vectordb/collections/describe", body, &response, nil)
	if err == nil {
		return ports.VectorCollectionHealth{Ready: true}, nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return ports.VectorCollectionHealth{Ready: false, Reason: err.Error()}, nil
	}
	return ports.VectorCollectionHealth{}, err
}

func (s *MilvusVectorStore) doMilvus(ctx context.Context, path string, body map[string]any, output *milvusResponse, allow func(milvusResponse) bool) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint.ResolveReference(&url.URL{Path: path}).String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.doRequest(req)
	if err != nil {
		return err
	}
	defer closeBody(resp.Body)
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return milvusHTTPError(resp.StatusCode, string(bodyBytes))
	}
	var decoded milvusResponse
	if len(strings.TrimSpace(string(bodyBytes))) > 0 {
		if err := json.Unmarshal(bodyBytes, &decoded); err != nil {
			return err
		}
	}
	if decoded.Code != 0 {
		if allow != nil && allow(decoded) {
			return nil
		}
		return milvusCodeError(decoded)
	}
	if output != nil {
		*output = decoded
	}
	return nil
}

func (s *MilvusVectorStore) doRequest(req *http.Request) (*http.Response, error) {
	var lastErr error
	for index, endpoint := range s.endpoints {
		candidate, err := requestForMilvusEndpoint(req, endpoint)
		if err != nil {
			return nil, err
		}
		resp, err := s.doRequestOnce(candidate)
		if err != nil {
			lastErr = err
			if index < len(s.endpoints)-1 && resilience.Retryable(err) {
				continue
			}
			return nil, err
		}
		if index < len(s.endpoints)-1 && milvusRetryableStatus(resp.StatusCode) {
			lastErr = milvusHTTPError(resp.StatusCode, "")
			closeBody(resp.Body)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func (s *MilvusVectorStore) doRequestOnce(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	err := resilience.Do(req.Context(), s.policy, func(callCtx context.Context) error {
		var err error
		resp, err = s.client.Do(req.Clone(callCtx))
		return err
	})
	return resp, err
}

func requestForMilvusEndpoint(req *http.Request, endpoint *url.URL) (*http.Request, error) {
	candidate := req.Clone(req.Context())
	target := *endpoint
	target.Path = req.URL.Path
	target.RawPath = ""
	target.RawQuery = req.URL.RawQuery
	candidate.URL = &target
	candidate.Host = target.Host
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		candidate.Body = body
	}
	return candidate, nil
}

func (s *MilvusVectorStore) collectionPayload(ref ports.VectorCollectionRef) map[string]any {
	body := map[string]any{"collectionName": s.collectionName(ref)}
	if s.database != "" {
		body["dbName"] = s.database
	}
	return body
}

func (s *MilvusVectorStore) collectionName(ref ports.VectorCollectionRef) string {
	raw := s.collectionPrefix + strings.TrimSpace(ref.TenantID) + "_" + strings.TrimSpace(ref.KBID)
	safe := milvusCollectionSafePattern.ReplaceAllString(raw, "_")
	safe = strings.Trim(safe, "_")
	if safe == "" {
		return "ani_vector_collection"
	}
	if len(safe) > 255 {
		return safe[:255]
	}
	return safe
}

func parseMilvusEndpoint(raw string) (*url.URL, error) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return nil, fmt.Errorf("%w: Milvus endpoint is required", ports.ErrInvalid)
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid Milvus endpoint: %v", ports.ErrInvalid, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: Milvus endpoint scheme must be http or https", ports.ErrInvalid)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: Milvus endpoint host is required", ports.ErrInvalid)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func parseMilvusEndpoints(primary string, values []string) ([]*url.URL, error) {
	rawValues := append([]string{}, values...)
	if strings.TrimSpace(primary) != "" {
		rawValues = append([]string{primary}, rawValues...)
	}
	if len(rawValues) == 0 {
		return nil, fmt.Errorf("%w: Milvus endpoint is required", ports.ErrInvalid)
	}
	endpoints := make([]*url.URL, 0, len(rawValues))
	seen := map[string]struct{}{}
	for _, raw := range rawValues {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		parsed, err := parseMilvusEndpoint(trimmed)
		if err != nil {
			return nil, err
		}
		key := parsed.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		endpoints = append(endpoints, parsed)
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("%w: Milvus endpoint is required", ports.ErrInvalid)
	}
	return endpoints, nil
}

type milvusResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func milvusAllowAlreadyExists(response milvusResponse) bool {
	message := strings.ToLower(response.Message)
	return strings.Contains(message, "already exist")
}

func milvusCodeError(response milvusResponse) error {
	message := strings.TrimSpace(response.Message)
	if message == "" {
		message = fmt.Sprintf("Milvus returned code %d", response.Code)
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "not found"):
		return fmt.Errorf("%w: %s", ports.ErrNotFound, message)
	case strings.Contains(lower, "illegal") || strings.Contains(lower, "invalid"):
		return fmt.Errorf("%w: %s", ports.ErrInvalid, message)
	default:
		return fmt.Errorf("milvus returned code %d: %s", response.Code, message)
	}
}

func milvusHTTPError(statusCode int, body string) error {
	switch statusCode {
	case http.StatusNotFound:
		return ports.ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: Milvus HTTP %d", ports.ErrFailedPrecondition, statusCode)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: Milvus HTTP %d: %s", ports.ErrInvalid, statusCode, strings.TrimSpace(body))
	case http.StatusServiceUnavailable:
		return fmt.Errorf("%w: Milvus HTTP %d: %s", ports.ErrUnavailable, statusCode, strings.TrimSpace(body))
	default:
		return fmt.Errorf("milvus HTTP %d: %s", statusCode, strings.TrimSpace(body))
	}
}

func milvusRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func milvusSearchResults(data json.RawMessage) []ports.VectorSearchResult {
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	results := make([]ports.VectorSearchResult, 0, len(raw))
	for _, item := range raw {
		id, _ := item["id"].(string)
		score := float32Number(item["score"])
		if score == 0 {
			score = float32Number(item["distance"])
		}
		metadata := map[string]string{}
		if values, ok := item["metadata"].(map[string]any); ok {
			for key, value := range values {
				metadata[key] = fmt.Sprint(value)
			}
		}
		if content, ok := item["content"].(string); ok && content != "" {
			metadata["content"] = content
		}
		results = append(results, ports.VectorSearchResult{ID: id, Score: score, Metadata: metadata})
	}
	return results
}

func milvusDeleteCount(data json.RawMessage) int {
	var payload struct {
		DeleteCount int `json:"deleteCount"`
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return 0
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0
	}
	if payload.DeleteCount < 0 {
		return 0
	}
	return payload.DeleteCount
}

func float32Number(value any) float32 {
	switch typed := value.(type) {
	case float64:
		return float32(typed)
	case float32:
		return typed
	case int:
		return float32(typed)
	default:
		return 0
	}
}

func milvusFilter(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("metadata[\"%s\"] == \"%s\"", milvusEscape(key), milvusEscape(values[key])))
	}
	return strings.Join(parts, " and ")
}

func milvusIDFilter(ids []string) string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			values = append(values, `"`+milvusEscape(trimmed)+`"`)
		}
	}
	return "id in [" + strings.Join(values, ",") + "]"
}

func milvusEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func closeBody(body io.Closer) {
	if body != nil {
		_ = body.Close()
	}
}
