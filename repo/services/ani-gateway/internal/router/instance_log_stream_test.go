package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/network"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// mockNetworkConn 是 network.Conn 的最小 mock，仅支持写入（WriteBinary/Flush）。
// 读操作返回错误（SSE handler 只写入不读取）。
type mockNetworkConn struct {
	buf    bytes.Buffer
	closed bool
}

func (c *mockNetworkConn) WriteBinary(b []byte) (int, error) {
	return c.buf.Write(b)
}
func (c *mockNetworkConn) Flush() error { return nil }
func (c *mockNetworkConn) Malloc(n int) ([]byte, error) {
	return make([]byte, n), nil
}
func (c *mockNetworkConn) Read(b []byte) (int, error)            { return 0, net.ErrClosed }
func (c *mockNetworkConn) Peek(n int) ([]byte, error)            { return nil, net.ErrClosed }
func (c *mockNetworkConn) Skip(n int) error                      { return net.ErrClosed }
func (c *mockNetworkConn) Release() error                        { return nil }
func (c *mockNetworkConn) Len() int                              { return 0 }
func (c *mockNetworkConn) ReadByte() (byte, error)               { return 0, net.ErrClosed }
func (c *mockNetworkConn) ReadBinary(n int) ([]byte, error)      { return nil, net.ErrClosed }
func (c *mockNetworkConn) Write(b []byte) (int, error)           { return c.buf.Write(b) }
func (c *mockNetworkConn) Close() error                          { c.closed = true; return nil }
func (c *mockNetworkConn) LocalAddr() net.Addr                   { return nil }
func (c *mockNetworkConn) RemoteAddr() net.Addr                  { return nil }
func (c *mockNetworkConn) SetDeadline(t time.Time) error         { return nil }
func (c *mockNetworkConn) SetReadDeadline(t time.Time) error     { return nil }
func (c *mockNetworkConn) SetWriteDeadline(t time.Time) error    { return nil }
func (c *mockNetworkConn) SetReadTimeout(t time.Duration) error  { return nil }
func (c *mockNetworkConn) SetWriteTimeout(t time.Duration) error { return nil }

// 确保 mockNetworkConn 实现 network.Conn
var _ network.Conn = (*mockNetworkConn)(nil)

// ── 预流错误测试（不涉及 Hijack） ──────────────────────────────────────

// setupLogStreamTestServer 构造一个带/不带 observability 的实例路由 HTTP server。
func setupLogStreamTestServer(observability ports.InstanceObservability) *server.Hertz {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), observability, false, nil, nil)
	return h
}

// createInstanceForStreamTest 通过 HTTP 创建一个 container 实例并返回 ID。
func createInstanceForStreamTest(t *testing.T, h *server.Hertz, name, idempotencyKey string) string {
	t.Helper()
	createBody := `{"kind":"container","name":"` + name + `","idempotency_key":"` + idempotencyKey + `"}`
	createResp := ut.PerformRequest(h.Engine, "POST",
		"/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != 201 {
		t.Fatalf("create instance status = %d, body = %s", createResp.StatusCode(), string(createResp.Body()))
	}
	var created struct {
		Instance struct {
			ID string `json:"id"`
		} `json:"instance"`
	}
	if err := json.Unmarshal(createResp.Body(), &created); err != nil || created.Instance.ID == "" {
		t.Fatalf("parse created instance: err=%v body=%s", err, string(createResp.Body()))
	}
	return created.Instance.ID
}

// startRealHertzServer 在空闲端口启动真实 hertz server（Hijack 仅在真实网络下生效）。
func startRealHertzServer(t *testing.T, observability ports.InstanceObservability) (*server.Hertz, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	h := server.New(server.WithHostPorts(addr))
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), observability, false, nil, nil)
	go func() { _ = h.Run() }()
	// 等 server 就绪
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return h, addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("hertz server did not start within 5s")
	return nil, addr
}

// TestStreamInstanceLogs_NotConfiguredSendsSSEError 验证 local profile
// （StreamLogs 返回 ErrNotConfigured）时客户端仍立即收到 SSE 响应头，
// 随后以 event:error 帧告知错误并关闭（不再返回 503 JSON）。
// 必须用真实 server：SSE 头由 Hijack 连接上的手写响应返回，ut 环境无法触达。
func TestStreamInstanceLogs_NotConfiguredSendsSSEError(t *testing.T) {
	// 传 nil observability → fallback 到 local adapter → StreamLogs 返回 ErrNotConfigured
	h, addr := startRealHertzServer(t, nil)
	_ = h // server 生命周期随测试进程结束

	instanceID := createInstanceForStreamTest(t, h, "obs-nil-test", "obs-nil-create")

	resp, err := http.Get("http://" + addr + "/api/v1/instances/" + instanceID + "/logs/stream")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// SSE 头立即写出：即使未配置 Loki 也必须先拿到 200 + text/event-stream
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(string(body), "event: error") {
		t.Fatalf("body = %q, want event: error frame", string(body))
	}
}

// TestStreamInstanceLogs_SSEStreamEndToEnd 端到端验证 SSE 流：
// mock Loki → LokiLogStore → PrometheusInstanceObservability → gateway Hijack SSE。
// 首屏回放 2 条日志（时间正序），随后客户端主动断开。
func TestStreamInstanceLogs_SSEStreamEndToEnd(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	// mock Loki：backward 返回 2 条日志（最新在前），forward 返回空
	lokiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("direction") == "backward" {
			resp := map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "streams",
					"result": []map[string]any{
						{
							"stream": map[string]string{"namespace": "ani-tenant-tenant-a", "pod": "inst-e2e", "container": "main"},
							"values": [][]string{
								{fmt.Sprintf("%d", base.Add(-30*time.Second).UnixNano()), `{"level":"info","message":"stream-line-2"}`},
								{fmt.Sprintf("%d", base.Add(-60*time.Second).UnixNano()), `{"level":"info","message":"stream-line-1"}`},
							},
						},
					},
				},
			}
			b, _ := json.Marshal(resp)
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer lokiSrv.Close()

	lokiStore, err := runtimeadapter.NewLokiLogStore(runtimeadapter.LokiLogStoreConfig{
		BaseURL: lokiSrv.URL,
	})
	if err != nil {
		t.Fatalf("NewLokiLogStore: %v", err)
	}
	obs, err := runtimeadapter.NewPrometheusInstanceObservability(runtimeadapter.PrometheusInstanceObservabilityConfig{
		PrometheusURL:     "http://127.0.0.1:9090",
		KubernetesAPIHost: "https://127.0.0.1:6443",
	})
	if err != nil {
		t.Fatalf("NewPrometheusInstanceObservability: %v", err)
	}
	obs.SetLogStore(lokiStore)

	h, addr := startRealHertzServer(t, obs)
	_ = h // server 生命周期随测试进程结束

	instanceID := createInstanceForStreamTest(t, h, "inst-e2e", "inst-e2e-create")

	resp, err := http.Get("http://" + addr + "/api/v1/instances/" + instanceID + "/logs/stream?interval_seconds=1")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	// 读取 SSE 帧直到收到 2 条 log 事件
	reader := bufio.NewReader(resp.Body)
	var frames []string
	deadline := time.After(5 * time.Second)
	var frameBuf strings.Builder
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for SSE frames; got: %v", frames)
		default:
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read stream: %v; frames=%v", readErr, frames)
		}
		frameBuf.WriteString(line)
		if line == "\n" {
			frame := frameBuf.String()
			frameBuf.Reset()
			if strings.Contains(frame, "event: log") {
				frames = append(frames, frame)
				if len(frames) == 2 {
					_ = resp.Body.Close()
					goto done
				}
			}
		}
	}
done:
	// 首屏回放时间正序：line1 在前，line2 在后
	if !strings.Contains(frames[0], "stream-line-1") {
		t.Fatalf("first frame should contain stream-line-1: %q", frames[0])
	}
	if !strings.Contains(frames[1], "stream-line-2") {
		t.Fatalf("second frame should contain stream-line-2: %q", frames[1])
	}
}

// TestStreamInstanceLogs_EmptyReplayStillWritesHeadersImmediately 回归用例：
// Loki 回放为空（实例无历史日志）时，SSE 响应头也必须立即写出，
// 客户端马上拿到 200 + text/event-stream，随后进入增量等待——
// 不能等首条日志才写头（会导致连接零字节挂起，前端表现为“连接无反应”）。
func TestStreamInstanceLogs_EmptyReplayStillWritesHeadersImmediately(t *testing.T) {
	// mock Loki：backward / forward 全部返回空
	lokiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer lokiSrv.Close()

	lokiStore, err := runtimeadapter.NewLokiLogStore(runtimeadapter.LokiLogStoreConfig{
		BaseURL: lokiSrv.URL,
	})
	if err != nil {
		t.Fatalf("NewLokiLogStore: %v", err)
	}
	obs, err := runtimeadapter.NewPrometheusInstanceObservability(runtimeadapter.PrometheusInstanceObservabilityConfig{
		PrometheusURL:     "http://127.0.0.1:9090",
		KubernetesAPIHost: "https://127.0.0.1:6443",
	})
	if err != nil {
		t.Fatalf("NewPrometheusInstanceObservability: %v", err)
	}
	obs.SetLogStore(lokiStore)

	h, addr := startRealHertzServer(t, obs)
	_ = h

	instanceID := createInstanceForStreamTest(t, h, "inst-empty", "inst-empty-create")

	resp, err := http.Get("http://" + addr + "/api/v1/instances/" + instanceID + "/logs/stream?interval_seconds=30")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	// http.Get 在收到响应头即返回；若头未写出会阻塞直到超时/卡死
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 immediately", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
}

// TestStreamInstanceLogs_InstanceNotFoundReturns404 验证实例不存在时返回 404。
func TestStreamInstanceLogs_InstanceNotFoundReturns404(t *testing.T) {
	// 构造一个有 observability 但没有对应实例的场景
	mockObs := &mockStreamObservability{}
	h := setupLogStreamTestServer(mockObs)
	resp := ut.PerformRequest(h.Engine, "GET",
		"/api/v1/instances/nonexistent-12345/logs/stream", nil).Result()
	if resp.StatusCode() != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
}

// TestStreamInstanceLogs_QueryParamValidation 验证 query 参数契约校验：
// 非法 level / limit 越界 / interval_seconds 越界 / 非整数返回 400
// （发生在实例存在性校验之后，实例不存在时 404 优先于 400）。
func TestStreamInstanceLogs_QueryParamValidation(t *testing.T) {
	h := setupLogStreamTestServer(&mockStreamObservability{})
	instanceID := createInstanceForStreamTest(t, h, "obs-param-test", "obs-param-create")

	cases := []struct {
		name   string
		path   string
		status int
	}{
		{"limit too large", "/api/v1/instances/" + instanceID + "/logs/stream?limit=5000", 400},
		{"interval too large", "/api/v1/instances/" + instanceID + "/logs/stream?interval_seconds=99", 400},
		{"bad level", "/api/v1/instances/" + instanceID + "/logs/stream?level=bogus", 400},
		{"non-integer limit", "/api/v1/instances/" + instanceID + "/logs/stream?limit=abc", 400},
		{"404 precedes 400", "/api/v1/instances/nonexistent-12345/logs/stream?limit=5000", 404},
	}
	for _, tc := range cases {
		resp := ut.PerformRequest(h.Engine, "GET", tc.path, nil).Result()
		if resp.StatusCode() != tc.status {
			t.Fatalf("%s: status = %d, want %d; body=%s", tc.name, resp.StatusCode(), tc.status, string(resp.Body()))
		}
	}
}

// ── sseLogSink 单元测试 ─────────────────────────────────────────────────

// TestSSELogSink_WritesHeadersAndLogFrame 验证首条 sink 写 SSE 头 + log 帧。
func TestSSELogSink_WritesHeadersAndLogFrame(t *testing.T) {
	conn := &mockNetworkConn{}
	sink := &sseLogSink{conn: conn}

	entry := ports.InstanceLogEntry{
		Timestamp: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Level:     "info",
		Message:   "hello world",
		Container: "main",
		Stream:    "stdout",
	}
	if err := sink.Write(entry); err != nil {
		t.Fatalf("Write error = %v", err)
	}

	output := conn.buf.String()
	// 首次写入应包含 SSE 头
	if !strings.Contains(output, "HTTP/1.1 200 OK") {
		t.Fatalf("missing HTTP status line in output: %q", output)
	}
	if !strings.Contains(output, "text/event-stream") {
		t.Fatalf("missing Content-Type text/event-stream: %q", output)
	}
	// 应包含 event: log 帧
	if !strings.Contains(output, "event: log") {
		t.Fatalf("missing event: log frame: %q", output)
	}
	if !strings.Contains(output, "hello world") {
		t.Fatalf("missing message in log frame: %q", output)
	}
	// 头只写一次
	if sink.headWritten != true {
		t.Fatalf("headWritten = false, want true")
	}
}

// TestSSELogSink_SecondWriteSkipsHeaders 验证后续 sink 只写 log 帧，不重复写头。
func TestSSELogSink_SecondWriteSkipsHeaders(t *testing.T) {
	conn := &mockNetworkConn{}
	sink := &sseLogSink{conn: conn}

	entry1 := ports.InstanceLogEntry{Timestamp: time.Now(), Level: "info", Message: "first"}
	entry2 := ports.InstanceLogEntry{Timestamp: time.Now(), Level: "info", Message: "second"}

	_ = sink.Write(entry1)
	conn.buf.Reset() // 清空，只看第二次写入
	_ = sink.Write(entry2)

	output := conn.buf.String()
	if strings.Contains(output, "HTTP/1.1") {
		t.Fatalf("second write should not contain headers: %q", output)
	}
	if !strings.Contains(output, "event: log") {
		t.Fatalf("missing event: log frame: %q", output)
	}
	if !strings.Contains(output, "second") {
		t.Fatalf("missing message: %q", output)
	}
}

// TestSSELogSink_WriteSSEError 验证 error 帧格式。
func TestSSELogSink_WriteSSEError(t *testing.T) {
	conn := &mockNetworkConn{}
	sink := &sseLogSink{conn: conn, headWritten: true}

	sink.writeSSEError("loki connection lost")

	output := conn.buf.String()
	if !strings.Contains(output, "event: error") {
		t.Fatalf("missing event: error: %q", output)
	}
	if !strings.Contains(output, "loki connection lost") {
		t.Fatalf("missing error message: %q", output)
	}
}

// TestSSELogSink_WriteSSEDone 验证 done 帧格式。
func TestSSELogSink_WriteSSEDone(t *testing.T) {
	conn := &mockNetworkConn{}
	sink := &sseLogSink{conn: conn, headWritten: true}

	sink.writeSSEDone("timeout")

	output := conn.buf.String()
	if !strings.Contains(output, "event: done") {
		t.Fatalf("missing event: done: %q", output)
	}
	if !strings.Contains(output, "timeout") {
		t.Fatalf("missing reason: %q", output)
	}
}

// ── mock observability ────────────────────────────────────────────────────

// mockStreamObservability 是 ports.InstanceObservability 的空实现，
// 只用于测试预流校验路径（instanceForObservation 先于 StreamLogs 执行）。
type mockStreamObservability struct{}

func (m *mockStreamObservability) ListLogs(context.Context, ports.InstanceObservationListRequest) (ports.InstanceLogListResult, error) {
	return ports.InstanceLogListResult{}, nil
}
func (m *mockStreamObservability) ListEvents(context.Context, ports.InstanceObservationListRequest) (ports.InstanceEventListResult, error) {
	return ports.InstanceEventListResult{}, nil
}
func (m *mockStreamObservability) GetMetrics(context.Context, ports.InstanceObservationGetRequest) (ports.InstanceMetricsRecord, error) {
	return ports.InstanceMetricsRecord{}, nil
}
func (m *mockStreamObservability) ListSecurityEvents(context.Context, ports.InstanceObservationListRequest) (ports.InstanceSecurityEventListResult, error) {
	return ports.InstanceSecurityEventListResult{}, nil
}
func (m *mockStreamObservability) StreamLogs(context.Context, ports.InstanceLogStreamRequest, func(ports.InstanceLogEntry) error) error {
	return nil
}
func (m *mockStreamObservability) CreateExecSession(context.Context, ports.InstanceExecSessionCreateRequest) (ports.InstanceExecSessionRecord, error) {
	return ports.InstanceExecSessionRecord{}, nil
}
func (m *mockStreamObservability) CreateConsoleSession(context.Context, ports.InstanceConsoleSessionCreateRequest) (ports.InstanceConsoleSessionRecord, error) {
	return ports.InstanceConsoleSessionRecord{}, nil
}

var _ ports.InstanceObservability = (*mockStreamObservability)(nil)
