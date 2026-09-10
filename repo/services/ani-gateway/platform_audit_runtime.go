package main

import (
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// defaultAuditLokiURL 是审计 Loki 的默认地址（与 gateway 既有
// INSTANCE_OBSERVABILITY_LOG_STORE=loki 的默认地址一致）。
const defaultAuditLokiURL = "http://ani-loki.ani-s07-observability:3100"

// newGatewayPlatformAuditService 装配平台审计日志服务：默认直连 Loki，
// 无需配置 provider env。当前方案是过渡方案，后续将由独立审计服务替代。
func newGatewayPlatformAuditService() (ports.PlatformAuditService, error) {
	return runtimeadapter.NewLokiPlatformAudit(runtimeadapter.LokiPlatformAuditConfig{
		BaseURL: auditLokiBaseURL(),
	})
}

// auditLokiBaseURL 读取 AUDIT_LOG_LOKI_URL，缺省即默认审计 Loki 地址。
func auditLokiBaseURL() string {
	if v := strings.TrimSpace(getenvAuditLokiURL()); v != "" {
		return v
	}
	return defaultAuditLokiURL
}

// getenvAuditLokiURL 读取 AUDIT_LOG_LOKI_URL 环境变量（main 包内薄封装便于测试）。
func getenvAuditLokiURL() string { return os.Getenv("AUDIT_LOG_LOKI_URL") }
