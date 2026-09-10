package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// TestLocalComponentDiagnosticsDeterministic 验证 local 降级实现的确定性输出：
// 指标固定值（1 核 / 256MiB / 512MiB / 1024B/s）、单条日志、dev_profile 为 local。
func TestLocalComponentDiagnosticsDeterministic(t *testing.T) {
	service := NewLocalPlatformComponentDiagnosticsService()

	metrics, err := service.GetComponentMetrics(context.Background(), "ani-auth-service")
	if err != nil {
		t.Fatalf("GetComponentMetrics() error = %v", err)
	}
	if metrics.Component.Name != "ani-auth-service" || metrics.Component.Namespace != "ani-system" {
		t.Fatalf("component ref = %+v, want ani-auth-service/ani-system", metrics.Component)
	}
	if metrics.CPUCores == nil || *metrics.CPUCores != 1.0 {
		t.Fatalf("CPUCores = %v, want 1.0", metrics.CPUCores)
	}
	if metrics.MemoryUsedMB == nil || *metrics.MemoryUsedMB != 256.0 {
		t.Fatalf("MemoryUsedMB = %v, want 256.0", metrics.MemoryUsedMB)
	}
	if metrics.MemoryTotalMB == nil || *metrics.MemoryTotalMB != 512.0 {
		t.Fatalf("MemoryTotalMB = %v, want 512.0", metrics.MemoryTotalMB)
	}
	if metrics.NetworkRxBytesPerSec == nil || *metrics.NetworkRxBytesPerSec != 1024.0 {
		t.Fatalf("NetworkRxBytesPerSec = %v, want 1024.0", metrics.NetworkRxBytesPerSec)
	}
	if metrics.DevProfile.Mode != "local" || metrics.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want local non-real", metrics.DevProfile)
	}

	logs, err := service.QueryComponentLogs(context.Background(), ports.PlatformComponentLogQueryRequest{Component: "ani-auth-service"})
	if err != nil {
		t.Fatalf("QueryComponentLogs() error = %v", err)
	}
	if logs.Total != 1 || len(logs.Items) != 1 {
		t.Fatalf("logs total = %d, want 1 deterministic entry", logs.Total)
	}
	if logs.Items[0].Level != "info" || logs.Items[0].Stream != "stdout" {
		t.Fatalf("log entry = %+v, want info/stdout", logs.Items[0])
	}
	if logs.DevProfile.Mode != "local" || logs.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want local non-real", logs.DevProfile)
	}
}

// TestLocalComponentDiagnosticsUnknownComponent 验证未注册组件三接口均返回 ErrNotFound。
func TestLocalComponentDiagnosticsUnknownComponent(t *testing.T) {
	service := NewLocalPlatformComponentDiagnosticsService()
	if _, err := service.GetComponentMetrics(context.Background(), "not-a-component"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetComponentMetrics error = %v, want ErrNotFound", err)
	}
	if _, err := service.QueryComponentLogs(context.Background(), ports.PlatformComponentLogQueryRequest{Component: "not-a-component"}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("QueryComponentLogs error = %v, want ErrNotFound", err)
	}
	if err := service.StreamComponentLogs(context.Background(), ports.PlatformComponentLogStreamRequest{Component: "not-a-component"}, func(ports.PlatformComponentLogEntry) error { return nil }); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("StreamComponentLogs error = %v, want ErrNotFound", err)
	}
}

// TestLocalComponentDiagnosticsStreamStopsOnSinkError 验证 sink 断开时流立即退出。
func TestLocalComponentDiagnosticsStreamStopsOnSinkError(t *testing.T) {
	service := NewLocalPlatformComponentDiagnosticsService()
	err := service.StreamComponentLogs(context.Background(), ports.PlatformComponentLogStreamRequest{
		Component: "ani-gateway",
	}, func(ports.PlatformComponentLogEntry) error {
		return errors.New("client disconnected")
	})
	if err != nil {
		t.Fatalf("StreamComponentLogs() error = %v, want nil on sink error", err)
	}
}
