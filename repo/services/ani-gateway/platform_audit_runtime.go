package main

import (
	"fmt"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// defaultAuditLokiURL 是审计 Loki 的默认地址（与 gateway 既有
// INSTANCE_OBSERVABILITY_LOG_STORE=loki 的默认地址一致）。
const defaultAuditLokiURL = "http://ani-loki.ani-s07-observability:3100"

// newGatewayPlatformAuditService 按 env 装配平台审计日志服务。
// AUDIT_LOG_PROVIDER 取值：
//   - "" / "local" / "not_configured" → 返回 nil，router 回退 local 确定性降级；
//   - "loki" → 返回 real adapter（AUDIT_LOG_LOKI_URL 缺省即
//     http://ani-loki.ani-s07-observability:3100），构造失败返回 ErrUnsupported；
//   - 其他值 → 返回 ErrUnsupported。
//
// 只编译进 gateway 二进制，部署只需更新 gateway，无需改动其它服务。
func newGatewayPlatformAuditService(cfg LokiPlatformAuditConfig) (ports.PlatformAuditService, error) {
	mode := strings.TrimSpace(cfg.Provider)
	switch mode {
	case "", "local", "not_configured":
		return nil, nil
	case "loki":
		return runtimeadapter.NewLokiPlatformAudit(runtimeadapter.LokiPlatformAuditConfig{
			BaseURL: cfg.BaseURL,
		})
	default:
		return nil, fmt.Errorf("%w: unsupported AUDIT_LOG_PROVIDER %q", ports.ErrPlatformAuditUnsupported, mode)
	}
}

// auditLokiBaseURL 读取 AUDIT_LOG_LOKI_URL，缺省即默认审计 Loki 地址。
func auditLokiBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("AUDIT_LOG_LOKI_URL")); v != "" {
		return v
	}
	return defaultAuditLokiURL
}

// LokiPlatformAuditConfig 是 audit runtime 的装配配置（env 派生）。
type LokiPlatformAuditConfig struct {
	Provider string
	BaseURL  string
}
