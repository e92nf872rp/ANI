package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// mutableClock 是可在测试中推进的时钟。
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *mutableClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// lokiStreamTestServer 构造一个模拟 Loki query_range 的 HTTP server。
func lokiStreamTestServer(t *testing.T, handleFunc func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(handleFunc))
}

// lokiStreamValues 构造 Loki stream values（[ts_ns, json_line]）。
func lokiStreamValues(ts time.Time, level, msg string) []string {
	return []string{fmt.Sprintf("%d", ts.UnixNano()), fmt.Sprintf(`{"level":"%s","message":"%s"}`, level, msg)}
}

func lokiStreamResponse(values [][]string) string {
	resp := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "streams",
			"result": []map[string]any{
				{
					"stream": map[string]string{"namespace": "ani-tenant-test", "pod": "inst-test-abc", "container": "main"},
					"values": values,
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func emptyLokiStreamResponse() string {
	return `{"status":"success","data":{"resultType":"streams","result":[]}}`
}

// newLokiStreamStore 构造一个指向 testServer 的 LokiLogStore。
func newLokiStreamStore(t *testing.T, baseURL string, nowFunc func() time.Time) *LokiLogStore {
	t.Helper()
	store, err := NewLokiLogStore(LokiLogStoreConfig{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		Now:        nowFunc,
	})
	if err != nil {
		t.Fatalf("NewLokiLogStore failed: %v", err)
	}
	return store
}

// TestStreamLogs_ReplayToIncrementalHandoff 验证回放→增量的游标衔接。
func TestStreamLogs_ReplayToIncrementalHandoff(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: base}
	var callCount int32

	srv := lokiStreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		direction := r.URL.Query().Get("direction")
		n := atomic.AddInt32(&callCount, 1)

		if direction == "backward" {
			// backward 返回最新在前（与 Loki direction=backward 语义一致）
			values := [][]string{
				lokiStreamValues(base.Add(-1*time.Minute), "info", "line1"),
				lokiStreamValues(base.Add(-2*time.Minute), "info", "line2"),
				lokiStreamValues(base.Add(-3*time.Minute), "info", "line3"),
			}
			fmt.Fprint(w, lokiStreamResponse(values))
		} else {
			// forward：第 2 次调用时时钟已推进，返回在 [lastTS+1ns, now] 范围内的新日志
			if n == 2 {
				values := [][]string{
					lokiStreamValues(base.Add(1*time.Second), "info", "new-line"),
				}
				fmt.Fprint(w, lokiStreamResponse(values))
			} else {
				fmt.Fprint(w, emptyLokiStreamResponse())
			}
		}
	})
	defer srv.Close()

	store := newLokiStreamStore(t, srv.URL, clock.Now)

	var collected []ports.InstanceLogEntry
	sink := func(entry ports.InstanceLogEntry) error {
		collected = append(collected, entry)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// 等回放完成后推进时钟，使 forward 能查到新日志
		for atomic.LoadInt32(&callCount) < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		clock.Advance(2 * time.Second) // now = base+2s，使 base+1s 的新日志在范围内
		// 等 forward 查到新日志后取消
		for atomic.LoadInt32(&callCount) < 3 {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()

	err := store.StreamLogs(ctx, ports.InstanceLogStreamRequest{
		TenantID:        "test",
		InstanceID:      "inst-test",
		Limit:           100,
		IntervalSeconds: 1,
	}, "ani-tenant-test", sink)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	if len(collected) != 4 {
		t.Fatalf("expected 4 entries, got %d: %+v", len(collected), collected)
	}
	if collected[0].Message != "line3" || collected[3].Message != "new-line" {
		t.Fatalf("unexpected order: %+v", collected)
	}
	seen := make(map[string]bool)
	for _, e := range collected {
		if seen[e.Message] {
			t.Fatalf("duplicate entry: %s", e.Message)
		}
		seen[e.Message] = true
	}
}

// TestStreamLogs_ForwardDedup 验证 forward 轮询结果去重。
func TestStreamLogs_ForwardDedup(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: base}
	var callCount int32

	srv := lokiStreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		direction := r.URL.Query().Get("direction")
		atomic.AddInt32(&callCount, 1)

		if direction == "backward" {
			fmt.Fprint(w, emptyLokiStreamResponse())
		} else {
			// 每次 forward 都返回同一条日志
			values := [][]string{
				lokiStreamValues(base.Add(1*time.Second), "info", "dup-line"),
			}
			fmt.Fprint(w, lokiStreamResponse(values))
		}
	})
	defer srv.Close()

	store := newLokiStreamStore(t, srv.URL, clock.Now)

	var collected []ports.InstanceLogEntry
	sink := func(entry ports.InstanceLogEntry) error {
		collected = append(collected, entry)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// 推进时钟使 forward 查询范围有效
		for atomic.LoadInt32(&callCount) < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		clock.Advance(5 * time.Second)
		// 等 2 次 forward 后取消
		for atomic.LoadInt32(&callCount) < 3 {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()

	err := store.StreamLogs(ctx, ports.InstanceLogStreamRequest{
		TenantID:        "test",
		InstanceID:      "inst-test",
		Limit:           100,
		IntervalSeconds: 1,
	}, "ani-tenant-test", sink)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	if len(collected) != 1 {
		t.Fatalf("expected 1 entry (dedup), got %d: %+v", len(collected), collected)
	}
	if collected[0].Message != "dup-line" {
		t.Fatalf("unexpected message: %s", collected[0].Message)
	}
}

// TestStreamLogs_LevelFilter 验证 level 过滤。
func TestStreamLogs_LevelFilter(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: base}
	var callCount int32

	srv := lokiStreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			values := [][]string{
				lokiStreamValues(base.Add(-2*time.Minute), "info", "info-line"),
				lokiStreamValues(base.Add(-1*time.Minute), "error", "error-line"),
			}
			fmt.Fprint(w, lokiStreamResponse(values))
		} else {
			fmt.Fprint(w, emptyLokiStreamResponse())
		}
	})
	defer srv.Close()

	store := newLokiStreamStore(t, srv.URL, clock.Now)

	var collected []ports.InstanceLogEntry
	sink := func(entry ports.InstanceLogEntry) error {
		collected = append(collected, entry)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for atomic.LoadInt32(&callCount) < 2 {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()

	err := store.StreamLogs(ctx, ports.InstanceLogStreamRequest{
		TenantID:        "test",
		InstanceID:      "inst-test",
		Level:           "error",
		Limit:           100,
		IntervalSeconds: 1,
	}, "ani-tenant-test", sink)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	if len(collected) != 1 {
		t.Fatalf("expected 1 entry (level filter), got %d: %+v", len(collected), collected)
	}
	if collected[0].Level != "error" || collected[0].Message != "error-line" {
		t.Fatalf("unexpected entry: %+v", collected[0])
	}
}

// TestStreamLogs_SinkDisconnectExits 验证 sink 断开立即退出。
func TestStreamLogs_SinkDisconnectExits(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: base}

	srv := lokiStreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		direction := r.URL.Query().Get("direction")
		if direction == "backward" {
			values := [][]string{
				lokiStreamValues(base.Add(-3*time.Minute), "info", "line1"),
				lokiStreamValues(base.Add(-2*time.Minute), "info", "line2"),
				lokiStreamValues(base.Add(-1*time.Minute), "info", "line3"),
			}
			fmt.Fprint(w, lokiStreamResponse(values))
		} else {
			fmt.Fprint(w, emptyLokiStreamResponse())
		}
	})
	defer srv.Close()

	store := newLokiStreamStore(t, srv.URL, clock.Now)

	var callCount int
	sink := func(entry ports.InstanceLogEntry) error {
		callCount++
		if callCount == 2 {
			return fmt.Errorf("client disconnected")
		}
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- store.StreamLogs(context.Background(), ports.InstanceLogStreamRequest{
			TenantID:        "test",
			InstanceID:      "inst-test",
			Limit:           100,
			IntervalSeconds: 1,
		}, "ani-tenant-test", sink)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil on sink disconnect, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamLogs did not exit on sink disconnect within 2s")
	}
	if callCount != 2 {
		t.Fatalf("expected 2 sink calls, got %d", callCount)
	}
}

// TestStreamLogs_LokiTransientFailureSelfHeals 验证 Loki 瞬时失败下一周期自愈。
func TestStreamLogs_LokiTransientFailureSelfHeals(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: base}
	var callCount int32
	var fwdCount int32

	srv := lokiStreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		direction := r.URL.Query().Get("direction")
		atomic.AddInt32(&callCount, 1)
		if direction == "backward" {
			fmt.Fprint(w, emptyLokiStreamResponse())
		} else {
			n := atomic.AddInt32(&fwdCount, 1)
			if n == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"status":"error","error":"internal"}`)
			} else if n == 2 {
				values := [][]string{
					lokiStreamValues(base.Add(6*time.Second), "info", "after-fail"),
				}
				fmt.Fprint(w, lokiStreamResponse(values))
			} else {
				fmt.Fprint(w, emptyLokiStreamResponse())
			}
		}
	})
	defer srv.Close()

	store := newLokiStreamStore(t, srv.URL, clock.Now)

	var collected []ports.InstanceLogEntry
	sink := func(entry ports.InstanceLogEntry) error {
		collected = append(collected, entry)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// 等回放完成后再推进时钟，使 forward 查询窗口 [base+1ns, base+10s] 有效
		for atomic.LoadInt32(&callCount) < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		clock.Advance(10 * time.Second) // now=base+10s，lastTS=base，forward 能查到 base+6s 的日志
		for atomic.LoadInt32(&callCount) < 3 {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()

	err := store.StreamLogs(ctx, ports.InstanceLogStreamRequest{
		TenantID:        "test",
		InstanceID:      "inst-test",
		Limit:           100,
		IntervalSeconds: 1,
	}, "ani-tenant-test", sink)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	if len(collected) != 1 {
		t.Fatalf("expected 1 entry after self-heal, got %d: %+v", len(collected), collected)
	}
	if collected[0].Message != "after-fail" {
		t.Fatalf("unexpected message: %s", collected[0].Message)
	}
}

// TestStreamLogs_EmptyReplaySetsCursorToNow 验证回放为空时 lastTS=now。
func TestStreamLogs_EmptyReplaySetsCursorToNow(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: base}

	srv := lokiStreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, emptyLokiStreamResponse())
	})
	defer srv.Close()

	store := newLokiStreamStore(t, srv.URL, clock.Now)

	var collected []ports.InstanceLogEntry
	sink := func(entry ports.InstanceLogEntry) error {
		collected = append(collected, entry)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := store.StreamLogs(ctx, ports.InstanceLogStreamRequest{
		TenantID:        "test",
		InstanceID:      "inst-test",
		Limit:           100,
		IntervalSeconds: 1,
	}, "ani-tenant-test", sink)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	if len(collected) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(collected))
	}
}

// TestStreamLogs_IdentityValidation 验证租户/实例 ID 校验。
func TestStreamLogs_IdentityValidation(t *testing.T) {
	srv := lokiStreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {})
	defer srv.Close()

	store := newLokiStreamStore(t, srv.URL, time.Now)

	err := store.StreamLogs(context.Background(), ports.InstanceLogStreamRequest{
		TenantID:   "",
		InstanceID: "inst-test",
	}, "ani-tenant-test", func(ports.InstanceLogEntry) error { return nil })
	if !strings.Contains(err.Error(), "tenant_id is required") {
		t.Fatalf("expected tenant_id error, got %v", err)
	}

	err = store.StreamLogs(context.Background(), ports.InstanceLogStreamRequest{
		TenantID:   "test",
		InstanceID: "",
	}, "ani-tenant-test", func(ports.InstanceLogEntry) error { return nil })
	if !strings.Contains(err.Error(), "instance_id is required") {
		t.Fatalf("expected instance_id error, got %v", err)
	}
}
