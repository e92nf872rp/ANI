package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

const defaultCoreAPIBaseURL = "http://127.0.0.1:8080/api/v1"

// defaultCoreAPITimeout 是 tenant-service 调 Core API 的 HTTP 超时。
// anisdk.Client（自动生成）内部使用 http.DefaultClient.Do 发请求且不接收 context，
// 无法通过 SDK 选项注入自定义 http.Client 或 per-request deadline。
// 这里在 init() 中设置 http.DefaultClient.Timeout，使所有 SDK 调用都有超时保护，
// 避免 Core 挂起时 tenant-service handler goroutine 无限阻塞。
const defaultCoreAPITimeout = 10 * time.Second

func init() {
	http.DefaultClient.Timeout = defaultCoreAPITimeout
}

func newCoreSDKClient() anisdk.Client {
	base := strings.TrimSpace(os.Getenv("CORE_API_BASE_URL"))
	if base == "" {
		base = defaultCoreAPIBaseURL
	}
	return anisdk.NewClient(strings.TrimRight(base, "/"), strings.TrimSpace(os.Getenv("CORE_API_TOKEN")))
}

func idempotencyHeaders() (map[string]string, error) {
	key, err := anisdk.NewIdempotencyKey("ani")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ports.ErrCoreUnavailable, err)
	}
	return map[string]string{"Idempotency-Key": key}, nil
}

func mapSDKError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr anisdk.APIError
	if errors.As(err, &apiErr) {
		detail := strings.TrimSpace(apiErr.Message)
		if detail == "" {
			detail = apiErr.Code
		}
		switch strings.TrimSpace(apiErr.Code) {
		case ports.ErrTenantNotFound.Error():
			return fmt.Errorf("%w: %s", ports.ErrTenantNotFound, detail)
		case ports.ErrTenantPlanNotFound.Error():
			return fmt.Errorf("%w: %s", ports.ErrTenantPlanNotFound, detail)
		case ports.ErrQuotaNotFound.Error():
			return fmt.Errorf("%w: %s", ports.ErrQuotaNotFound, detail)
		case ports.ErrQuotaAlreadyExists.Error():
			return fmt.Errorf("%w: %s", ports.ErrQuotaAlreadyExists, detail)
		case ports.ErrQuotaResourceNotRegistered.Error():
			return fmt.Errorf("%w: %s", ports.ErrQuotaResourceNotRegistered, detail)
		case ports.ErrValidationFailed.Error():
			return fmt.Errorf("%w: %s", ports.ErrValidationFailed, detail)
		case "USER_NOT_FOUND":
			return fmt.Errorf("%w: %s", ports.ErrTenantAdminNotFound, detail)
		case ports.ErrTenantAdminNotFound.Error():
			return fmt.Errorf("%w: %s", ports.ErrTenantAdminNotFound, detail)
		case ports.ErrRoleChangeInvalid.Error():
			return fmt.Errorf("%w: %s", ports.ErrRoleChangeInvalid, detail)
		case ports.ErrPasswordSameAsOld.Error():
			return fmt.Errorf("%w: %s", ports.ErrPasswordSameAsOld, detail)
		default:
			return fmt.Errorf("%w: %s", ports.ErrCoreUnavailable, detail)
		}
	}
	return fmt.Errorf("%w: %v", ports.ErrCoreUnavailable, err)
}

func asObject(v any) (map[string]any, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: empty response", ports.ErrCoreUnavailable)
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	if s, ok := v.(string); ok {
		var out map[string]any
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("%w: decode object: %v", ports.ErrCoreUnavailable, err)
		}
		return out, nil
	}
	// SDK may decode numbers oddly; re-marshal for safety.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ports.ErrCoreUnavailable, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: decode object: %v", ports.ErrCoreUnavailable, err)
	}
	return out, nil
}

func asObjectSlice(v any) ([]map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: expected array", ports.ErrCoreUnavailable)
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		obj, err := asObject(it)
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func boolField(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func int64Field(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}
