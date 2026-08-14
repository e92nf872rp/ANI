package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

const (
	defaultCoreAPIBaseURL = "http://127.0.0.1:8080/api/v1"
	defaultHTTPTimeout    = 5 * time.Second
)

// QuotaSvcClient 是 ports.QuotaSvcClient 的 Core API HTTP 客户端实现。
type QuotaSvcClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// 编译期断言：确保 QuotaSvcClient 满足 ports.QuotaSvcClient 接口。
var _ ports.QuotaSvcClient = (*QuotaSvcClient)(nil)

// NewQuotaSvcClient 从环境变量构造 Core 配额 API 客户端。
// CORE_API_BASE_URL 默认 http://127.0.0.1:8080/api/v1；CORE_API_TOKEN 可选 Bearer。
func NewQuotaSvcClient() ports.QuotaSvcClient {
	// 步骤 1：解析 CORE_API_BASE_URL
	base := strings.TrimSpace(os.Getenv("CORE_API_BASE_URL"))
	if base == "" {
		base = defaultCoreAPIBaseURL
	}
	// 步骤 2：装配 HTTP 客户端
	return &QuotaSvcClient{
		baseURL: strings.TrimRight(base, "/"),
		token:   strings.TrimSpace(os.Getenv("CORE_API_TOKEN")),
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// ListQuotaMeta 每次远程调用 Core GET /admin/quota-meta，返回启用维度（无本地缓存）。
func (c *QuotaSvcClient) ListQuotaMeta(ctx context.Context) ([]ports.QuotaMeta, error) {
	// 步骤 1：GET /admin/quota-meta
	url := c.baseURL + "/admin/quota-meta"
	body, err := c.doJSON(ctx, http.MethodGet, url, nil, false)
	if err != nil {
		return nil, err
	}

	// 步骤 2：反序列化 QuotaMetaListResponse
	var decoded struct {
		Items []struct {
			ResourceType string `json:"resource_type"`
			DisplayName  string `json:"display_name"`
			Unit         string `json:"unit"`
			DefaultQuota int64  `json:"default_quota"`
			IsDiscrete   bool   `json:"is_discrete"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ports.ErrCoreUnavailable, err)
	}

	// 步骤 3：映射为 ports.QuotaMeta（Core 仅返回 enabled 维度 → Enabled 固定 true）
	out := make([]ports.QuotaMeta, 0, len(decoded.Items))
	for _, it := range decoded.Items {
		rt := strings.TrimSpace(it.ResourceType)
		if rt == "" {
			continue
		}
		out = append(out, ports.QuotaMeta{
			ResourceType: rt,
			Enabled:      true,
			DefaultQuota: it.DefaultQuota,
			DisplayName:  it.DisplayName,
			Unit:         it.Unit,
			IsDiscrete:   it.IsDiscrete,
		})
	}
	return out, nil
}

// GetQuota 查询租户配额视图。
func (c *QuotaSvcClient) GetQuota(ctx context.Context, tenantID uuid.UUID) ([]ports.CoreQuotaResult, error) {
	// 步骤 1：GET /admin/tenants/{id}/quota
	url := fmt.Sprintf("%s/admin/tenants/%s/quota", c.baseURL, tenantID.String())
	body, err := c.doJSON(ctx, http.MethodGet, url, nil, false)
	if err != nil {
		return nil, err
	}
	// 步骤 2：解析 items（含 tightened；GET 通常为 false）
	return decodeQuotaItems(body)
}

// PutQuota 批量修改租户配额上限；tightened=true 不视为错误。
func (c *QuotaSvcClient) PutQuota(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	// 步骤 1：组装 PUT 请求体
	url := fmt.Sprintf("%s/admin/tenants/%s/quota", c.baseURL, tenantID.String())
	payload := map[string]any{"items": encodeQuotaItems(items)}

	// 步骤 2：带 Idempotency-Key 调用 Core
	body, err := c.doJSON(ctx, http.MethodPut, url, payload, true)
	if err != nil {
		return nil, err
	}

	// 步骤 3：解析结果（tightened 由调用方忽略/观测，不报错）
	return decodeQuotaItems(body)
}

// CreateQuota 批量新建租户配额行。
func (c *QuotaSvcClient) CreateQuota(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	// 步骤 1：组装 POST 请求体
	url := fmt.Sprintf("%s/admin/tenants/%s/quota", c.baseURL, tenantID.String())
	payload := map[string]any{"items": encodeQuotaItems(items)}

	// 步骤 2：带 Idempotency-Key 调用 Core
	body, err := c.doJSON(ctx, http.MethodPost, url, payload, true)
	if err != nil {
		return nil, err
	}

	// 步骤 3：解析新建结果
	return decodeQuotaItems(body)
}

// DeleteQuota 删除租户全部配额行。
func (c *QuotaSvcClient) DeleteQuota(ctx context.Context, tenantID uuid.UUID) error {
	// 步骤 1：DELETE /admin/tenants/{id}/quota（带幂等键）
	url := fmt.Sprintf("%s/admin/tenants/%s/quota", c.baseURL, tenantID.String())
	_, err := c.doJSON(ctx, http.MethodDelete, url, nil, true)
	return err
}

func encodeQuotaItems(items []ports.CoreQuotaItem) []map[string]any {
	// 步骤 1：ports.CoreQuotaItem → Core JSON items
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"resource_type": it.ResourceType,
			"total":         it.Total,
		})
	}
	return out
}

func decodeQuotaItems(body []byte) ([]ports.CoreQuotaResult, error) {
	// 步骤 1：反序列化 Quota.items
	var decoded struct {
		Items []struct {
			ResourceType string `json:"resource_type"`
			Total        int64  `json:"total"`
			Used         int64  `json:"used"`
			Reserved     int64  `json:"reserved"`
			Tightened    bool   `json:"tightened"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ports.ErrCoreUnavailable, err)
	}

	// 步骤 2：映射为 ports.CoreQuotaResult
	out := make([]ports.CoreQuotaResult, 0, len(decoded.Items))
	for _, it := range decoded.Items {
		out = append(out, ports.CoreQuotaResult{
			ResourceType: it.ResourceType,
			Total:        it.Total,
			Used:         it.Used,
			Reserved:     it.Reserved,
			Tightened:    it.Tightened,
		})
	}
	return out, nil
}

// doJSON 发 JSON 请求；needIdempotency 为 true 时自动带 Idempotency-Key。
func (c *QuotaSvcClient) doJSON(ctx context.Context, method, url string, payload any, needIdempotency bool) ([]byte, error) {
	// 步骤 1：可选 JSON body
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("%w: marshal: %v", ports.ErrCoreUnavailable, err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	// 步骤 2：构造请求头（Accept / Content-Type / Idempotency-Key / Authorization）
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ports.ErrCoreUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if needIdempotency {
		req.Header.Set("Idempotency-Key", uuid.New().String())
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// 步骤 3：发起调用；网络错误 → ErrCoreUnavailable
	slog.Debug("core quota request", "method", method, "url", url)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ports.ErrCoreUnavailable, err)
	}
	defer resp.Body.Close()

	// 步骤 4：读 body；非 2xx → 业务码映射
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ports.ErrCoreUnavailable, err)
	}
	if resp.StatusCode >= 400 {
		return nil, mapCoreHTTPError(resp.StatusCode, body)
	}
	return body, nil
}

func mapCoreHTTPError(status int, body []byte) error {
	// 步骤 1：优先解析 ErrorResponse.code
	var errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &errBody)
	code := strings.TrimSpace(errBody.Code)
	detail := strings.TrimSpace(errBody.Message)
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}

	// 步骤 2：按业务码精确映射
	switch code {
	case ports.ErrTenantNotFound.Error():
		return fmt.Errorf("%w: %s", ports.ErrTenantNotFound, detail)
	case ports.ErrQuotaNotFound.Error():
		return fmt.Errorf("%w: %s", ports.ErrQuotaNotFound, detail)
	case ports.ErrQuotaAlreadyExists.Error():
		return fmt.Errorf("%w: %s", ports.ErrQuotaAlreadyExists, detail)
	case ports.ErrQuotaResourceNotRegistered.Error():
		return fmt.Errorf("%w: %s", ports.ErrQuotaResourceNotRegistered, detail)
	case ports.ErrValidationFailed.Error():
		return fmt.Errorf("%w: %s", ports.ErrValidationFailed, detail)
	}

	// 步骤 3：无业务码时按 HTTP status 兜底
	switch status {
	case http.StatusNotFound:
		// PUT 缺配额行 → QUOTA_NOT_FOUND；GET/DELETE 租户缺失 → TENANT_NOT_FOUND
		if strings.Contains(strings.ToLower(detail), "quota") {
			return fmt.Errorf("%w: %s", ports.ErrQuotaNotFound, detail)
		}
		return fmt.Errorf("%w: %s", ports.ErrTenantNotFound, detail)
	case http.StatusConflict:
		return fmt.Errorf("%w: %s", ports.ErrQuotaAlreadyExists, detail)
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %s", ports.ErrQuotaResourceNotRegistered, detail)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ports.ErrValidationFailed, detail)
	default:
		return fmt.Errorf("%w: status %d", ports.ErrCoreUnavailable, status)
	}
}
