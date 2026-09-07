// component_diagnostics.go 实现平台组件诊断只读接口（平台健康组件状态行内
// 「指标 / 日志」下钻）：
//   - GET /api/v1/platform/components/{component_name}/metrics（资源指标快照）
//   - GET /api/v1/platform/components/{component_name}/logs（Loki 日志列表）
//   - GET /api/v1/platform/components/{component_name}/logs/stream（SSE 日志流）
//
// SSE 帧语义与 /instances/{instance_id}/logs/stream 一致（event: log/error/done）。
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// componentDiagnosticsAPI 组件指标 + 日志双端口 handler。
type componentDiagnosticsAPI struct {
	metrics ports.PlatformComponentMetricsReader
	logs    ports.PlatformComponentLogReader
}

type componentMetricsResponse struct {
	Component            componentRefResponse   `json:"component"`
	Timestamp            string                 `json:"timestamp"`
	CPUCores             *float64               `json:"cpu_cores"`
	MemoryUsedMB         *float64               `json:"memory_used_mb"`
	MemoryTotalMB        *float64               `json:"memory_total_mb"`
	NetworkRxBytesPerSec *float64               `json:"network_rx_bytes_per_sec"`
	NetworkTxBytesPerSec *float64               `json:"network_tx_bytes_per_sec"`
	DevProfile           coreDevProfileResponse `json:"dev_profile"`
}

type componentRefResponse struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Group     string `json:"group"`
}

type componentLogEntryResponse struct {
	Timestamp string  `json:"timestamp"`
	Level     string  `json:"level"`
	Message   string  `json:"message"`
	Pod       *string `json:"pod"`
	Container *string `json:"container"`
	Stream    *string `json:"stream"`
}

type componentLogListResponse struct {
	Items      []componentLogEntryResponse `json:"items"`
	Total      int                         `json:"total"`
	NextCursor *string                     `json:"next_cursor"`
	DevProfile coreDevProfileResponse      `json:"dev_profile"`
}

// newComponentDiagnosticsAPI 注入为 nil 时回退 local 确定性实现（与组件状态
// fallback 惯例一致），保证 gateway 未配置 provider 时仍可启动并返回 200。
func newComponentDiagnosticsAPI(metrics ports.PlatformComponentMetricsReader, logs ports.PlatformComponentLogReader) *componentDiagnosticsAPI {
	if metrics == nil || logs == nil {
		local := runtimeadapter.NewLocalPlatformComponentDiagnosticsService()
		if metrics == nil {
			metrics = local
		}
		if logs == nil {
			logs = local
		}
	}
	return &componentDiagnosticsAPI{metrics: metrics, logs: logs}
}

func registerComponentDiagnostics(v1 *route.RouterGroup, metrics ports.PlatformComponentMetricsReader, logs ports.PlatformComponentLogReader) {
	api := newComponentDiagnosticsAPI(metrics, logs)
	v1.GET("/platform/components/:component_name/metrics", api.getComponentMetrics)
	v1.GET("/platform/components/:component_name/logs", api.listComponentLogs)
	v1.GET("/platform/components/:component_name/logs/stream", api.streamComponentLogs)
}

// writeComponentDiagnosticsError 按错误语义映射 HTTP 状态：
// ErrInvalid → 400、ErrNotFound → 404、ErrNotConfigured → 503、其余 → 500。
func writeComponentDiagnosticsError(c *app.RequestContext, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, ports.ErrInvalid):
		code = http.StatusBadRequest
	case errors.Is(err, ports.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, ports.ErrNotConfigured):
		code = http.StatusServiceUnavailable
	}
	writeInstanceError(c, code, "COMPONENT_DIAGNOSTICS_FAILED", err.Error())
}

// getComponentMetrics 是 GET /platform/components/:component_name/metrics handler。
func (api *componentDiagnosticsAPI) getComponentMetrics(ctx context.Context, c *app.RequestContext) {
	metrics, err := api.metrics.GetComponentMetrics(ctx, c.Param("component_name"))
	if err != nil {
		writeComponentDiagnosticsError(c, err)
		return
	}
	response := componentMetricsResponse{
		Timestamp:            metrics.Timestamp.UTC().Format(time.RFC3339),
		CPUCores:             metrics.CPUCores,
		MemoryUsedMB:         metrics.MemoryUsedMB,
		MemoryTotalMB:        metrics.MemoryTotalMB,
		NetworkRxBytesPerSec: metrics.NetworkRxBytesPerSec,
		NetworkTxBytesPerSec: metrics.NetworkTxBytesPerSec,
		DevProfile: coreDevProfileResponse{
			Mode:         metrics.DevProfile.Mode,
			Provider:     metrics.DevProfile.Provider,
			RealProvider: metrics.DevProfile.RealProvider,
			Reason:       metrics.DevProfile.Reason,
		},
		Component: componentRefResponse{
			Name:      metrics.Component.Name,
			Namespace: metrics.Component.Namespace,
			Group:     metrics.Component.Group,
		},
	}
	c.JSON(http.StatusOK, response)
}

// listComponentLogs 是 GET /platform/components/:component_name/logs handler。
func (api *componentDiagnosticsAPI) listComponentLogs(ctx context.Context, c *app.RequestContext) {
	request := ports.PlatformComponentLogQueryRequest{
		Component: c.Param("component_name"),
		Level:     strings.TrimSpace(c.Query("level")),
		Cursor:    strings.TrimSpace(c.Query("cursor")),
	}
	if request.Level != "" && !streamLevelEnum[request.Level] {
		writeComponentDiagnosticsError(c, invalidComponentQuery("level must be one of debug/info/warn/error"))
		return
	}
	limit, err := parseStreamQueryInt(c, "limit", 100, 1, 1000)
	if err != nil {
		writeComponentDiagnosticsError(c, err)
		return
	}
	request.Limit = limit

	result, err := api.logs.QueryComponentLogs(ctx, request)
	if err != nil {
		writeComponentDiagnosticsError(c, err)
		return
	}
	c.JSON(http.StatusOK, componentLogListResponseFromResult(result))
}

// streamComponentLogs 是 GET /platform/components/:component_name/logs/stream
// SSE handler，连接管理与帧语义复刻 instance log stream（10 分钟上限、
// 立即写头 + 注释帧保代理 flush、client 断开感知）。
func (api *componentDiagnosticsAPI) streamComponentLogs(ctx context.Context, c *app.RequestContext) {
	component := c.Param("component_name")
	request := ports.PlatformComponentLogStreamRequest{
		Component: component,
		Level:     strings.TrimSpace(c.Query("level")),
	}
	if request.Level != "" && !streamLevelEnum[request.Level] {
		writeComponentDiagnosticsError(c, invalidComponentQuery("level must be one of debug/info/warn/error"))
		return
	}
	limit, err := parseStreamQueryInt(c, "limit", 1000, 1, 1000)
	if err != nil {
		writeComponentDiagnosticsError(c, err)
		return
	}
	interval, err := parseStreamQueryInt(c, "interval_seconds", 2, 1, 30)
	if err != nil {
		writeComponentDiagnosticsError(c, err)
		return
	}
	request.Limit = limit
	request.IntervalSeconds = interval

	// 预流校验：组件未注册等错误在进入 SSE 前以 JSON 返回。
	// 通过一次 dry-run registry 校验实现（StreamComponentLogs 内部同样校验）。
	if _, err := runtimeadapter.ValidatePlatformComponentRegistered(component); err != nil {
		writeComponentDiagnosticsError(c, err)
		return
	}

	c.Response.SetStatusCode(http.StatusOK)
	c.Response.HijackWriter(&noopExtWriter{})

	c.Hijack(func(conn network.Conn) {
		defer func() { _ = conn.Close() }()
		streamCtx, cancel := context.WithTimeout(context.Background(), logStreamTimeout)
		defer cancel()

		sink := &componentLogSSESink{conn: conn}
		if _, err := conn.WriteBinary([]byte(sseHeaders)); err != nil {
			return
		}
		if _, err := conn.WriteBinary([]byte(sseConnectedComment)); err != nil {
			return
		}
		if err := conn.Flush(); err != nil {
			return
		}
		sink.headWritten = true

		if err := api.logs.StreamComponentLogs(streamCtx, request, sink.Write); err != nil {
			slog.Error("component log stream failed",
				"component", request.Component, "error", err.Error())
			sink.writeSSEError(err.Error())
			return
		}
		reason := "closed"
		if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
			reason = "timeout"
		}
		sink.writeSSEDone(reason)
	})
}

// componentLogSSESink 封装组件日志 SSE 写出（帧语义同 sseLogSink）。
type componentLogSSESink struct {
	conn        network.Conn
	headWritten bool
}

// Write 实现 sink 回调：写 event:log 帧（payload 带 pod 维度）。
func (s *componentLogSSESink) Write(entry ports.PlatformComponentLogEntry) error {
	if !s.headWritten {
		if _, err := s.conn.WriteBinary([]byte(sseHeaders)); err != nil {
			return err
		}
		s.headWritten = true
	}
	data, _ := json.Marshal(map[string]any{
		"timestamp": entry.Timestamp.Format(time.RFC3339Nano),
		"level":     entry.Level,
		"message":   entry.Message,
		"pod":       entry.Pod,
		"container": entry.Container,
		"stream":    entry.Stream,
	})
	frame := "event: log\ndata: " + string(data) + "\n\n"
	if _, err := s.conn.WriteBinary([]byte(frame)); err != nil {
		return err
	}
	return s.conn.Flush()
}

func (s *componentLogSSESink) writeSSEError(message string) {
	data, _ := json.Marshal(map[string]string{"code": "LOG_STREAM_ERROR", "message": message})
	_, _ = s.conn.WriteBinary([]byte("event: error\ndata: " + string(data) + "\n\n"))
	_ = s.conn.Flush()
}

func (s *componentLogSSESink) writeSSEDone(reason string) {
	data, _ := json.Marshal(map[string]string{"reason": reason})
	_, _ = s.conn.WriteBinary([]byte("event: done\ndata: " + string(data) + "\n\n"))
	_ = s.conn.Flush()
}

func invalidComponentQuery(message string) error {
	return fmt.Errorf("%w: %s", ports.ErrInvalid, message)
}

func componentLogListResponseFromResult(result ports.PlatformComponentLogListResult) componentLogListResponse {
	items := make([]componentLogEntryResponse, 0, len(result.Items))
	for _, entry := range result.Items {
		item := componentLogEntryResponse{
			Timestamp: entry.Timestamp.UTC().Format(time.RFC3339),
			Level:     entry.Level,
			Message:   entry.Message,
		}
		if entry.Pod != "" {
			pod := entry.Pod
			item.Pod = &pod
		}
		if entry.Container != "" {
			container := entry.Container
			item.Container = &container
		}
		if entry.Stream != "" {
			stream := entry.Stream
			item.Stream = &stream
		}
		items = append(items, item)
	}
	response := componentLogListResponse{
		Items: items,
		Total: result.Total,
		DevProfile: coreDevProfileResponse{
			Mode:         result.DevProfile.Mode,
			Provider:     result.DevProfile.Provider,
			RealProvider: result.DevProfile.RealProvider,
			Reason:       result.DevProfile.Reason,
		},
	}
	if result.NextCursor != "" {
		cursor := result.NextCursor
		response.NextCursor = &cursor
	}
	return response
}
