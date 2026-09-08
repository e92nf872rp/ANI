package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// LocalPlatformComponentDiagnosticsService 组件诊断 local 确定性降级实现：
// metrics 固定资源值（1 核 / 256MiB / limit 512MiB / 网络 1024B/s），logs 返回
// 单条确定性日志、流式仅回放一条后随 ctx 退出。用于 provider 未配置时回退，
// 保证接口可启动可联调；dev_profile 明确标注 local 非 real。
type LocalPlatformComponentDiagnosticsService struct{}

// 编译时断言实现两个诊断端口。
var (
	_ ports.PlatformComponentMetricsReader = (*LocalPlatformComponentDiagnosticsService)(nil)
	_ ports.PlatformComponentLogReader     = (*LocalPlatformComponentDiagnosticsService)(nil)
)

// NewLocalPlatformComponentDiagnosticsService 构造 local 确定性实现。
func NewLocalPlatformComponentDiagnosticsService() *LocalPlatformComponentDiagnosticsService {
	return &LocalPlatformComponentDiagnosticsService{}
}

// GetComponentMetrics 返回确定性组件指标快照。
func (s *LocalPlatformComponentDiagnosticsService) GetComponentMetrics(ctx context.Context, component string) (ports.PlatformComponentMetrics, error) {
	entry, err := componentRegistryEntry(component)
	if err != nil {
		return ports.PlatformComponentMetrics{}, err
	}
	cpu, memUsed, memTotal := 1.0, 256.0, 512.0
	net := 1024.0
	return ports.PlatformComponentMetrics{
		Component:            ports.PlatformComponentRef{Name: entry.Name, Namespace: entry.Namespace, Group: entry.Group},
		Timestamp:            time.Now().UTC(),
		CPUCores:             &cpu,
		MemoryUsedMB:         &memUsed,
		MemoryTotalMB:        &memTotal,
		NetworkRxBytesPerSec: &net,
		NetworkTxBytesPerSec: &net,
		DevProfile: ports.DevProfileInfo{
			Mode:         "local",
			Provider:     localPlatformComponentStatusProvider,
			RealProvider: false,
			Reason:       "deterministic local fixture",
		},
	}, nil
}

// QueryComponentLogs 返回单条确定性组件日志。
func (s *LocalPlatformComponentDiagnosticsService) QueryComponentLogs(ctx context.Context, request ports.PlatformComponentLogQueryRequest) (ports.PlatformComponentLogListResult, error) {
	entry, err := componentRegistryEntry(request.Component)
	if err != nil {
		return ports.PlatformComponentLogListResult{}, err
	}
	items := []ports.PlatformComponentLogEntry{localComponentLogEntry(entry.Name)}
	return ports.PlatformComponentLogListResult{
		Items: items,
		Total: len(items),
		DevProfile: ports.DevProfileInfo{
			Mode:         "local",
			Provider:     localPlatformComponentStatusProvider,
			RealProvider: false,
			Reason:       "deterministic local fixture",
		},
	}, nil
}

// StreamComponentLogs 回放一条确定性日志后随 ctx 退出。
func (s *LocalPlatformComponentDiagnosticsService) StreamComponentLogs(ctx context.Context, request ports.PlatformComponentLogStreamRequest, sink func(ports.PlatformComponentLogEntry) error) error {
	entry, err := componentRegistryEntry(request.Component)
	if err != nil {
		return err
	}
	if err := sink(localComponentLogEntry(entry.Name)); err != nil {
		return nil
	}
	<-ctx.Done()
	return nil
}

func localComponentLogEntry(component string) ports.PlatformComponentLogEntry {
	return ports.PlatformComponentLogEntry{
		Timestamp: time.Now().UTC(),
		Level:     "info",
		Message:   fmt.Sprintf("deterministic local log for component %s", component),
		Pod:       component + "-local",
		Container: component,
		Stream:    "stdout",
	}
}
