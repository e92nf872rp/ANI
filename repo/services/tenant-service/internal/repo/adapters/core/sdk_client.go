package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/metadata"
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

// coreMinter 是全局可选的动态 token minter；为 nil 时所有请求回退
// CORE_API_TOKEN 静态 token（双层设计的兜底层）。由 SetupMinter 在进程启动时注入。
var coreMinter *Minter

// SetupMinter 注入 auth-service 动态 mint 客户端；addr/secret 任一为空返回 nil
// 表示保持静态兜底。main.go 双 env（AUTH_SERVICE_GRPC_ADDR + AUTH_SERVICE_MINT_SECRET）
// 都非空时才调用。
func SetupMinter(addr, secret string) (*Minter, error) {
	addr = strings.TrimSpace(addr)
	secret = strings.TrimSpace(secret)
	if addr == "" || secret == "" {
		return nil, nil
	}
	minter, err := DialMinter(addr, secret)
	if err != nil {
		return nil, err
	}
	coreMinter = minter
	return minter, nil
}

// applyAuthToken 给请求注入访问 Core 的凭证（双层设计）：
// minter 可用时注入动态 mint 的 JWT（per-request Authorization 覆盖 SDK Token 字段），
// 否则保持 SDK 构造时的静态 CORE_API_TOKEN。
func applyAuthToken(ctx context.Context, headers map[string]string) (map[string]string, error) {
	if coreMinter == nil {
		return headers, nil
	}
	token, err := coreMinter.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: mint core token: %v", ports.ErrCoreUnavailable, err)
	}
	if headers == nil {
		headers = map[string]string{}
	}
	// SDK Request() 中 options.Headers 在 client.Token 之后应用，可覆盖 Authorization。
	headers["Authorization"] = "Bearer " + token
	return headers, nil
}

// coreRequest 是所有 Core SDK 调用的统一入口：先经 applyAuthToken 注入凭证，
// 再透传 BOSS 请求头。minter 不可用时回退静态 token。
func coreRequest(ctx context.Context, sdk anisdk.Client, method, path string, opts anisdk.RequestOptions) (any, error) {
	headers, err := applyAuthToken(ctx, opts.Headers)
	if err != nil {
		return nil, err
	}
	opts.Headers = headers
	opts.Context = ctx
	return sdk.Request(method, path, opts)
}

// corePropagateHeaders 统一从 gRPC incoming metadata 组装调用 Core 的 HTTP 头。
// - X-Request-ID ← x-request-id（BOSS 网关注入）
// - X-ANI-Actor-User-ID ← x-user-id（BOSS 操作者）
// Core Gateway 再把这两头注入 ctx，供 PostgresTenant 写 tenant_lifecycle。
func corePropagateHeaders(ctx context.Context) map[string]string {
	headers := map[string]string{}
	if id := metadataFirst(ctx, "x-request-id"); id != "" {
		headers["X-Request-ID"] = id
	}
	if id := metadataFirst(ctx, "x-user-id"); id != "" {
		headers["X-ANI-Actor-User-ID"] = id
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func metadataFirst(ctx context.Context, key string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[0])
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
		case ports.ErrTenantNameConflict.Error():
			return fmt.Errorf("%w: %s", ports.ErrTenantNameConflict, detail)
		case ports.ErrTenantStateInvalid.Error():
			return fmt.Errorf("%w: %s", ports.ErrTenantStateInvalid, detail)
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
		case ports.ErrUserStateInvalid.Error():
			return fmt.Errorf("%w: %s", ports.ErrUserStateInvalid, detail)
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
