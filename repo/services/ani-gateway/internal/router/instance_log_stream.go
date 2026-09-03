package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/kubercloud/ani/pkg/ports"
)

// logStreamTimeout 是 SSE 日志流连接的固定时长上限（方案 §3.4：10 分钟）。
const logStreamTimeout = 10 * time.Minute

// streamLevelEnum 是 stream 接口 level 参数的契约枚举（openapi v1.yaml）。
var streamLevelEnum = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// parseStreamQueryInt 解析 query 整数参数并按契约范围校验；
// 非整数或越界返回包装 ports.ErrInvalid 的错误（由 writeInstanceObservabilityError 映射 400）。
func parseStreamQueryInt(c *app.RequestContext, name string, fallback, min, max int) (int, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", ports.ErrInvalid, name)
	}
	if value < min || value > max {
		return 0, fmt.Errorf("%w: %s must be between %d and %d", ports.ErrInvalid, name, min, max)
	}
	return value, nil
}

// streamInstanceLogs 是 SSE 流式日志 handler，注册在
// GET /api/v1/instances/{instance_id}/logs/stream。
//
// 执行流：
//  1. 预流校验：instanceForObservation（401/404 → JSON），observability=nil → 503 JSON
//  2. Hijack 接管连接：先不写响应头，等第一条 sink 回调时再写 SSE 头
//  3. 调用 observability.StreamLogs，sink 回调写 SSE event:log 帧
//  4. StreamLogs 返回 ErrNotConfigured 且未写头 → JSON 503
//  5. 10 分钟超时 → event:done{reason:"timeout"} 后关闭
//  6. 客户端断开（conn.Write 失败）→ cancel ctx → StreamLogs 退出
func (api *instanceAPI) streamInstanceLogs(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}

	level := strings.TrimSpace(c.Query("level"))
	if level != "" && !streamLevelEnum[level] {
		writeInstanceObservabilityError(c, fmt.Errorf("%w: level must be one of debug/info/warn/error", ports.ErrInvalid))
		return
	}
	limit, err := parseStreamQueryInt(c, "limit", 1000, 1, 1000)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	interval, err := parseStreamQueryInt(c, "interval_seconds", 2, 1, 30)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}

	req := ports.InstanceLogStreamRequest{
		TenantID:        instanceTenantID(c),
		InstanceID:      api.observabilityTargetID(record),
		Level:           level,
		Limit:           limit,
		IntervalSeconds: interval,
	}

	// 抑制 hertz 默认响应写出（否则客户端会收到两个 HTTP 响应）。
	// 技术细节：writeResponse 检测到 HijackWriter != nil 时只调 Finalize()，
	// 不写 headers/body，使 Hijack handler 获得干净的原始连接。
	c.Response.SetStatusCode(http.StatusOK)
	c.Response.HijackWriter(&noopExtWriter{})

	c.Hijack(func(conn network.Conn) {
		defer func() { _ = conn.Close() }()

		// 注意：不能使用 handler 的 ctx（hertz handler ctx 生命周期到 handler 返回为止，
		// Hijack 回调执行时它已被取消/复用——netpoll 下必现，会导致首个 Loki 查询以
		// ctx 取消失败、连接静默秒退）。这里改用独立 ctx；客户端断开由 sink 写出
		// 失败感知（StreamLogs 契约，方案 §3.4），轮询周期内最多延迟 interval 秒退出。
		streamCtx, cancel := context.WithTimeout(context.Background(), logStreamTimeout)
		defer cancel()

		sink := &sseLogSink{
			conn:        conn,
			headWritten: false,
		}

		// 立即写出 SSE 响应头：客户端必须马上拿到 200 + text/event-stream。
		// 不能等首条日志才写——回放为空的实例会让连接零字节挂起，
		// 前端表现为“连接无反应、看不到状态码”。
		// 注意 WriteBinary 只入缓冲，必须显式 Flush 才会真正发到网络上。
		if _, err := conn.WriteBinary([]byte(sseHeaders)); err != nil {
			return // 客户端已断开
		}
		// 紧跟一个 SSE 注释帧（RFC 冒号开头为注释，客户端不可见）：
		// node 系代理（vite dev proxy 等）的响应头要等首个 body 字节才向外 flush，
		// 纯头无 body 的流会卡在代理层，客户端看不到状态码。
		if _, err := conn.WriteBinary([]byte(sseConnectedComment)); err != nil {
			return
		}
		if err := conn.Flush(); err != nil {
			return
		}
		sink.headWritten = true

		err := api.observability.StreamLogs(streamCtx, req, sink.Write)

		if err != nil {
			// SSE 头已在进入流前写出，错误统一以 event:error 帧告知后关闭；
			// 预流错误（401/404/400/503）在 Hijack 之前由 JSON 路径处理。
			slog.Error("instance log stream failed",
				"tenant_id", req.TenantID, "instance_id", req.InstanceID,
				"head_written", sink.headWritten, "error", err.Error())
			sink.writeSSEError(err.Error())
			return
		}

		// 正常退出（ctx 超时或客户端断开）
		if sink.headWritten {
			reason := "closed"
			if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
				reason = "timeout"
			}
			sink.writeSSEDone(reason)
		}
	})
}

// sseLogSink 封装 SSE 流式写出的连接状态。
type sseLogSink struct {
	conn        network.Conn
	headWritten bool
}

// sseHeaders 是 SSE 流的 HTTP 响应头（手写，因为 Hijack 绕过了 hertz 默认响应）。
var sseHeaders = "HTTP/1.1 200 OK\r\n" +
	"Content-Type: text/event-stream\r\n" +
	"Cache-Control: no-cache\r\n" +
	"Connection: keep-alive\r\n" +
	"X-Accel-Buffering: no\r\n" +
	"\r\n"

// sseConnectedComment 是连接建立后立即发出的 SSE 注释帧：
// 冒号开头按 SSE 规范是注释，EventSource / fetch 解析器都会忽略，
// 但它作为首个 body 字节能让 node 系代理立刻 flush 响应头。
const sseConnectedComment = ": connected\n\n"

// Write 实现 sink 回调：首次调用写 SSE 头，然后写 event:log 帧。
func (s *sseLogSink) Write(entry ports.InstanceLogEntry) error {
	if !s.headWritten {
		if _, err := s.conn.WriteBinary([]byte(sseHeaders)); err != nil {
			return err // 客户端断开
		}
		s.headWritten = true
	}
	data, _ := json.Marshal(map[string]any{
		"timestamp": entry.Timestamp.Format(time.RFC3339Nano),
		"level":     entry.Level,
		"message":   entry.Message,
		"container": entry.Container,
		"stream":    entry.Stream,
	})
	frame := fmt.Sprintf("event: log\ndata: %s\n\n", string(data))
	if _, err := s.conn.WriteBinary([]byte(frame)); err != nil {
		return err // 客户端断开
	}
	return s.conn.Flush()
}

// writeSSEError 写出 event:error 帧。
func (s *sseLogSink) writeSSEError(message string) {
	data, _ := json.Marshal(map[string]string{
		"code":    "LOG_STREAM_ERROR",
		"message": message,
	})
	frame := fmt.Sprintf("event: error\ndata: %s\n\n", string(data))
	_, _ = s.conn.WriteBinary([]byte(frame))
	_ = s.conn.Flush()
}

// writeSSEDone 写出 event:done 帧。
func (s *sseLogSink) writeSSEDone(reason string) {
	data, _ := json.Marshal(map[string]string{
		"reason": reason,
	})
	frame := fmt.Sprintf("event: done\ndata: %s\n\n", string(data))
	_, _ = s.conn.WriteBinary([]byte(frame))
	_ = s.conn.Flush()
}

// noopExtWriter 是一个不写任何内容的 ExtWriter，用于抑制 hertz 默认响应写出。
// 设置后 writeResponse 只调 Finalize()（no-op），使 Hijack handler 获得干净的连接。
type noopExtWriter struct{}

func (w *noopExtWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *noopExtWriter) Flush() error                { return nil }
func (w *noopExtWriter) Finalize() error             { return nil }
