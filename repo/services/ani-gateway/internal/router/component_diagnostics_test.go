package router

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
)

// ── fake 实现：用于错误映射与参数校验测试 ─────────────────────────────

// fakeComponentMetricsReader 按指定错误返回（nil error 时返回空快照）。
type fakeComponentMetricsReader struct {
	err error
}

func (f *fakeComponentMetricsReader) GetComponentMetrics(context.Context, string) (ports.PlatformComponentMetrics, error) {
	return ports.PlatformComponentMetrics{}, f.err
}

// fakeComponentLogReader 按指定错误返回（nil error 时返回空结果）。
type fakeComponentLogReader struct {
	listErr   error
	streamErr error
}

func (f *fakeComponentLogReader) QueryComponentLogs(context.Context, ports.PlatformComponentLogQueryRequest) (ports.PlatformComponentLogListResult, error) {
	return ports.PlatformComponentLogListResult{}, f.listErr
}

func (f *fakeComponentLogReader) StreamComponentLogs(_ context.Context, _ ports.PlatformComponentLogStreamRequest, _ func(ports.PlatformComponentLogEntry) error) error {
	return f.streamErr
}

// setupComponentDiagnosticsTestServer 构造组件诊断路由 server（ut 环境用）。
func setupComponentDiagnosticsTestServer(metrics ports.PlatformComponentMetricsReader, logs ports.PlatformComponentLogReader) *server.Hertz {
	h := server.New()
	v1 := h.Group("/api/v1")
	registerComponentDiagnostics(v1, metrics, logs)
	return h
}

// startComponentDiagnosticsRealServer 在空闲端口启动真实 hertz server
// （SSE Hijack 仅在真实网络下生效，与 instance_log_stream_test 同模式）。
func startComponentDiagnosticsRealServer(t *testing.T, metrics ports.PlatformComponentMetricsReader, logs ports.PlatformComponentLogReader) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	h := server.New(server.WithHostPorts(addr))
	registerComponentDiagnostics(h.Group("/api/v1"), metrics, logs)
	go func() { _ = h.Run() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("hertz server did not start within 5s")
	return addr
}

// ── HTTP handler 测试（ut 环境） ─────────────────────────────────────

// TestGetComponentMetricsLocalFallback 验证未注入 reader 时回退 local 确定性
// 实现：200 + 固定指标值 + dev_profile.local。
func TestGetComponentMetricsLocalFallback(t *testing.T) {
	h := setupComponentDiagnosticsTestServer(nil, nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/platform/components/ani-auth-service/metrics", nil).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode(), string(resp.Body()))
	}
	body := string(resp.Body())
	for _, want := range []string{`"cpu_cores":1`, `"memory_used_mb":256`, `"memory_total_mb":512`, `"mode":"local"`, `"real_provider":false`, `"name":"ani-auth-service"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

// TestGetComponentMetricsUnknownComponent404 验证未注册组件返回 404。
func TestGetComponentMetricsUnknownComponent404(t *testing.T) {
	h := setupComponentDiagnosticsTestServer(nil, nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/platform/components/not-a-component/metrics", nil).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", resp.StatusCode(), string(resp.Body()))
	}
}

// TestGetComponentMetricsErrorMapping 验证 fake reader 错误到 HTTP 状态的映射：
// ErrInvalid → 400、ErrNotConfigured → 503、其他 → 500。
func TestGetComponentMetricsErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"invalid", ports.ErrInvalid, http.StatusBadRequest},
		{"not configured", ports.ErrNotConfigured, http.StatusServiceUnavailable},
		{"internal", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		h := setupComponentDiagnosticsTestServer(&fakeComponentMetricsReader{err: tc.err}, nil)
		resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/platform/components/ani-gateway/metrics", nil).Result()
		if resp.StatusCode() != tc.status {
			t.Fatalf("%s: status = %d, want %d; body = %s", tc.name, resp.StatusCode(), tc.status, string(resp.Body()))
		}
		if !strings.Contains(string(resp.Body()), "COMPONENT_DIAGNOSTICS_FAILED") {
			t.Fatalf("%s: body missing error code: %s", tc.name, string(resp.Body()))
		}
	}
}

// TestListComponentLogsLocalFallback 验证列表接口 local 回退返回单条确定性日志。
func TestListComponentLogsLocalFallback(t *testing.T) {
	h := setupComponentDiagnosticsTestServer(nil, nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/platform/components/ani-auth-service/logs?limit=10", nil).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode(), string(resp.Body()))
	}
	body := string(resp.Body())
	for _, want := range []string{`"total":1`, `"level":"info"`, `"pod":"ani-auth-service-local"`, `"mode":"local"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

// TestListComponentLogsQueryParamValidation 验证 query 参数校验：
// 非法 level → 400、limit 非整数 → 400、limit 越界 → 400、
// 未注册组件 404 优先于参数 400。
func TestListComponentLogsQueryParamValidation(t *testing.T) {
	h := setupComponentDiagnosticsTestServer(nil, nil)
	cases := []struct {
		name   string
		path   string
		status int
	}{
		{"bad level", "/api/v1/platform/components/ani-gateway/logs?level=bogus", 400},
		{"non-integer limit", "/api/v1/platform/components/ani-gateway/logs?limit=abc", 400},
		{"limit too large", "/api/v1/platform/components/ani-gateway/logs?limit=5000", 400},
		{"400 precedes 404", "/api/v1/platform/components/not-a-component/logs?limit=5000", 400},
		{"unknown component", "/api/v1/platform/components/not-a-component/logs?limit=10", 404},
	}
	for _, tc := range cases {
		resp := ut.PerformRequest(h.Engine, http.MethodGet, tc.path, nil).Result()
		if resp.StatusCode() != tc.status {
			t.Fatalf("%s: status = %d, want %d; body = %s", tc.name, resp.StatusCode(), tc.status, string(resp.Body()))
		}
	}
}

// TestListComponentLogsLokiNotConfigured503 验证日志端口未配置时返回 503。
func TestListComponentLogsLokiNotConfigured503(t *testing.T) {
	h := setupComponentDiagnosticsTestServer(nil, &fakeComponentLogReader{listErr: ports.ErrNotConfigured})
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/platform/components/ani-gateway/logs", nil).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", resp.StatusCode(), string(resp.Body()))
	}
}

// TestStreamComponentLogsQueryParamValidation 验证 SSE 预流参数校验（ut 环境，
// 不触达 Hijack）：非法 level / limit 越界 / interval 越界 → 400。
func TestStreamComponentLogsQueryParamValidation(t *testing.T) {
	h := setupComponentDiagnosticsTestServer(nil, nil)
	cases := []struct {
		name   string
		path   string
		status int
	}{
		{"bad level", "/api/v1/platform/components/ani-gateway/logs/stream?level=bogus", 400},
		{"limit too large", "/api/v1/platform/components/ani-gateway/logs/stream?limit=5000", 400},
		{"interval too large", "/api/v1/platform/components/ani-gateway/logs/stream?interval_seconds=99", 400},
		{"400 precedes 404", "/api/v1/platform/components/not-a-component/logs/stream?limit=5000", 400},
		{"unknown component", "/api/v1/platform/components/not-a-component/logs/stream", 404},
	}
	for _, tc := range cases {
		resp := ut.PerformRequest(h.Engine, http.MethodGet, tc.path, nil).Result()
		if resp.StatusCode() != tc.status {
			t.Fatalf("%s: status = %d, want %d; body = %s", tc.name, resp.StatusCode(), tc.status, string(resp.Body()))
		}
	}
}

// ── SSE 端到端测试（真实 server + Hijack） ───────────────────────────

// TestStreamComponentLogsSSEEndToEnd 端到端验证组件日志 SSE 流：
// local adapter 回放一条确定性日志（带 pod 维度）→ event:log 帧；
// 客户端断开后 handler 退出。同时验证首字节立即返回（SSE 头）。
func TestStreamComponentLogsSSEEndToEnd(t *testing.T) {
	addr := startComponentDiagnosticsRealServer(t, nil, nil)

	resp, err := http.Get("http://" + addr + "/api/v1/platform/components/ani-auth-service/logs/stream?interval_seconds=1")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	// 读取首条 event:log 帧（local adapter 回放一条确定性日志）。
	reader := bufio.NewReader(resp.Body)
	deadline := time.After(5 * time.Second)
	var frameBuf strings.Builder
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for log frame; got: %s", frameBuf.String())
		default:
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read stream: %v; got: %s", readErr, frameBuf.String())
		}
		frameBuf.WriteString(line)
		if line == "\n" && strings.Contains(frameBuf.String(), "event: log") {
			frame := frameBuf.String()
			for _, want := range []string{`"pod":"ani-auth-service-local"`, `"level":"info"`, "deterministic local log"} {
				if !strings.Contains(frame, want) {
					t.Fatalf("log frame missing %s: %q", want, frame)
				}
			}
			// 收到回放帧后主动断开，handler 随 sink error 退出。
			_ = resp.Body.Close()
			return
		}
	}
}
