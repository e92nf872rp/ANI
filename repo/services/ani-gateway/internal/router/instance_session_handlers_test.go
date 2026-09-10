package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type recordingInstanceSessionIssuer struct {
	execCalls    int
	consoleCalls int
	execRequest  ports.InstanceExecSessionCreateRequest
	consoleReq   ports.InstanceConsoleSessionCreateRequest
	err          error
}

func (s *recordingInstanceSessionIssuer) CreateExecSession(_ context.Context, request ports.InstanceExecSessionCreateRequest) (ports.InstanceExecSessionRecord, error) {
	s.execCalls++
	s.execRequest = request
	if s.err != nil {
		return ports.InstanceExecSessionRecord{}, s.err
	}
	return ports.InstanceExecSessionRecord{ID: "session-exec", InstanceID: request.InstanceID, WSURL: "wss://sessions.example/exec", ExpiresAt: time.Now().Add(time.Minute), DevProfile: sessionGatewayDevProfile()}, nil
}

func (s *recordingInstanceSessionIssuer) CreateConsoleSession(_ context.Context, request ports.InstanceConsoleSessionCreateRequest) (ports.InstanceConsoleSessionRecord, error) {
	s.consoleCalls++
	s.consoleReq = request
	if s.err != nil {
		return ports.InstanceConsoleSessionRecord{}, s.err
	}
	return ports.InstanceConsoleSessionRecord{SessionID: "session-console", InstanceID: request.InstanceID, Protocol: request.Protocol, ConnectURL: "wss://sessions.example/console", URL: "wss://sessions.example/console", ExpiresAt: time.Now().Add(time.Minute), DevProfile: sessionGatewayDevProfile()}, nil
}

func newInstanceSessionHandlerEngine(t *testing.T, kind string, issuer ports.InstanceSessionIssuer, denied bool) (*server.Hertz, string) {
	t.Helper()
	h := server.New()
	h.Use(middleware.RequestID())
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		if denied {
			c.JSON(http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
			c.Abort()
			return
		}
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithRuntime(h.Group("/api/v1"), nil, issuer, false, nil, nil, nil, nil, nil)
	if denied {
		return h, "denied-instance"
	}
	body := fmt.Sprintf(`{"kind":%q,"name":%q,"idempotency_key":%q}`, kind, "workload-"+kind, "create-"+kind)
	response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if response.StatusCode() != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.StatusCode(), response.Body())
	}
	return h, extractInstanceID(string(response.Body()))
}

func TestInstanceSessionHandlersMapAuthenticatedContextAndCompatibilityKeys(t *testing.T) {
	issuer := &recordingInstanceSessionIssuer{}
	h, instanceID := newInstanceSessionHandlerEngine(t, "gpu_container", issuer, false)
	body := `{"idempotency_key":"exec-key","container":"main","command":["/bin/sh"],"rows":30,"cols":120}`
	response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/exec", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "X-Request-ID", Value: "req-exec"}).Result()
	if response.StatusCode() != http.StatusOK {
		t.Fatalf("exec status=%d body=%s", response.StatusCode(), response.Body())
	}
	if !bytes.Contains(response.Body(), []byte(`"real_provider":true`)) || !bytes.Contains(response.Body(), []byte(`"ws_url":"wss://sessions.example/exec"`)) {
		t.Fatalf("exec response lost compatible session fields: %s", response.Body())
	}
	request := issuer.execRequest
	if request.RequestID != "req-exec" || request.IdempotencyKey != "exec-key" || request.TenantID != "tenant-a" || request.SubjectID != "user-a" || request.InstanceID != instanceID || request.WorkloadName != "workload-gpu_container" || request.WorkloadKind != ports.WorkloadKindGPUContainer {
		t.Fatalf("exec request = %+v", request)
	}

	consoleIssuer := &recordingInstanceSessionIssuer{}
	consoleEngine, vmID := newInstanceSessionHandlerEngine(t, "vm", consoleIssuer, false)
	consoleBody := `{"protocol":"serial"}`
	consoleResponse := ut.PerformRequest(consoleEngine.Engine, http.MethodPost, "/api/v1/instances/"+vmID+"/console", &ut.Body{Body: bytes.NewBufferString(consoleBody), Len: len(consoleBody)}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "X-Request-ID", Value: "req-console"}).Result()
	if consoleResponse.StatusCode() != http.StatusOK || consoleIssuer.consoleReq.IdempotencyKey == "" {
		t.Fatalf("console status=%d request=%+v body=%s", consoleResponse.StatusCode(), consoleIssuer.consoleReq, consoleResponse.Body())
	}
	if !bytes.Contains(consoleResponse.Body(), []byte(`"real_provider":true`)) || !bytes.Contains(consoleResponse.Body(), []byte(`"connect_url":"wss://sessions.example/console"`)) {
		t.Fatalf("console response lost compatible session fields: %s", consoleResponse.Body())
	}
	if consoleIssuer.consoleReq.IdempotencyKey == "req-console" {
		t.Fatal("console compatibility key must not reuse request_id")
	}
	if consoleIssuer.consoleReq.Protocol != "serial" || consoleIssuer.consoleReq.WorkloadKind != ports.WorkloadKindVM {
		t.Fatalf("console request = %+v", consoleIssuer.consoleReq)
	}

	providedKeyBody := `{"protocol":"vnc","idempotency_key":"console-key"}`
	providedKeyResponse := ut.PerformRequest(consoleEngine.Engine, http.MethodPost, "/api/v1/instances/"+vmID+"/console", &ut.Body{Body: bytes.NewBufferString(providedKeyBody), Len: len(providedKeyBody)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if providedKeyResponse.StatusCode() != http.StatusOK || consoleIssuer.consoleReq.IdempotencyKey != "console-key" {
		t.Fatalf("console explicit idempotency status=%d request=%+v body=%s", providedKeyResponse.StatusCode(), consoleIssuer.consoleReq, providedKeyResponse.Body())
	}
}

func TestInstanceSessionDeniedRequestsNeverCallIssuer(t *testing.T) {
	issuer := &recordingInstanceSessionIssuer{}
	h, instanceID := newInstanceSessionHandlerEngine(t, "container", issuer, true)
	for _, path := range []string{"/exec", "/console"} {
		body := `{"idempotency_key":"denied","protocol":"vnc"}`
		response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+path, &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
		if response.StatusCode() != http.StatusForbidden {
			t.Fatalf("%s status=%d", path, response.StatusCode())
		}
	}
	if issuer.execCalls != 0 || issuer.consoleCalls != 0 {
		t.Fatalf("issuer calls exec=%d console=%d", issuer.execCalls, issuer.consoleCalls)
	}
}

func TestExecPreconditionsRejectBeforeCallingIssuer(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		body string
		want int
	}{{"vm kind", "vm", `{"idempotency_key":"x"}`, 400}, {"empty command", "container", `{"idempotency_key":"x","command":[""]}`, 400}, {"negative rows", "container", `{"idempotency_key":"x","rows":-1}`, 400}, {"oversized cols", "container", `{"idempotency_key":"x","cols":4097}`, 400}} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := &recordingInstanceSessionIssuer{}
			h, instanceID := newInstanceSessionHandlerEngine(t, tc.kind, issuer, false)
			response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/exec", &ut.Body{Body: bytes.NewBufferString(tc.body), Len: len(tc.body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
			if response.StatusCode() != tc.want || issuer.execCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.StatusCode(), issuer.execCalls, response.Body())
			}
		})
	}
}

func TestConsolePreconditionsRejectBeforeCallingIssuer(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		body string
		want int
	}{{"container kind", "container", `{"protocol":"vnc"}`, 400}, {"invalid protocol", "vm", `{"protocol":"ssh"}`, 400}} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := &recordingInstanceSessionIssuer{}
			h, instanceID := newInstanceSessionHandlerEngine(t, tc.kind, issuer, false)
			response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/console", &ut.Body{Body: bytes.NewBufferString(tc.body), Len: len(tc.body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
			if response.StatusCode() != tc.want || issuer.consoleCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.StatusCode(), issuer.consoleCalls, response.Body())
			}
		})
	}
}

func TestConsoleStoppedInstanceRejectsBeforeCallingIssuer(t *testing.T) {
	issuer := &recordingInstanceSessionIssuer{}
	h, instanceID := newInstanceSessionHandlerEngine(t, "vm", issuer, false)
	stopBody := `{"action":"stop","idempotency_key":"stop-before-console"}`
	stop := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/lifecycle", &ut.Body{Body: bytes.NewBufferString(stopBody), Len: len(stopBody)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if stop.StatusCode() != http.StatusOK {
		t.Fatalf("stop status=%d body=%s", stop.StatusCode(), stop.Body())
	}
	consoleBody := `{"protocol":"vnc"}`
	response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/console", &ut.Body{Body: bytes.NewBufferString(consoleBody), Len: len(consoleBody)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if response.StatusCode() != http.StatusUnprocessableEntity || issuer.consoleCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.StatusCode(), issuer.consoleCalls, response.Body())
	}
}

func TestExecStoppedInstanceRejectsBeforeCallingIssuer(t *testing.T) {
	issuer := &recordingInstanceSessionIssuer{}
	h, instanceID := newInstanceSessionHandlerEngine(t, "container", issuer, false)
	stopBody := `{"action":"stop","idempotency_key":"stop-before-exec"}`
	stop := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/lifecycle", &ut.Body{Body: bytes.NewBufferString(stopBody), Len: len(stopBody)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if stop.StatusCode() != http.StatusOK {
		t.Fatalf("stop status=%d body=%s", stop.StatusCode(), stop.Body())
	}
	body := `{"idempotency_key":"exec-stopped"}`
	response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/exec", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if response.StatusCode() != http.StatusUnprocessableEntity || issuer.execCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.StatusCode(), issuer.execCalls, response.Body())
	}
}

func TestRealProviderWithoutSessionGatewayFailsClosed(t *testing.T) {
	injected := newInstanceAPI()
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithRuntime(h.Group("/api/v1"), nil, nil, false, nil, nil, nil, &InstanceRuntime{
		Service: injected.service, Store: injected.store, Operations: injected.operations, RealProvider: true, Provider: "kubernetes_rest",
	}, nil)
	createBody := `{"kind":"container","name":"real-without-session","idempotency_key":"create-real-without-session"}`
	created := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances", &ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if created.StatusCode() != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.StatusCode(), created.Body())
	}
	instanceID := extractInstanceID(string(created.Body()))
	body := `{"idempotency_key":"missing-session-gateway"}`
	response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/exec", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if response.StatusCode() != http.StatusServiceUnavailable || bytes.Contains(response.Body(), []byte("ws://")) {
		t.Fatalf("status=%d body=%s", response.StatusCode(), response.Body())
	}
}

func TestInstanceSessionErrorsUseStableHTTPEnvelope(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{{ports.ErrConflict, 409}, {ports.ErrFailedPrecondition, 422}, {ports.ErrSessionCapacity, 429}, {ports.ErrUnavailable, 503}, {ports.ErrSessionInternal, 500}, {errors.New("unexpected ticket=secret"), 500}} {
		t.Run(fmt.Sprint(tc.want), func(t *testing.T) {
			issuer := &recordingInstanceSessionIssuer{err: tc.err}
			h, instanceID := newInstanceSessionHandlerEngine(t, "container", issuer, false)
			body := `{"idempotency_key":"error-key"}`
			response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/exec", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
			if response.StatusCode() != tc.want || bytes.Contains(response.Body(), []byte("ticket=secret")) {
				t.Fatalf("status=%d body=%s", response.StatusCode(), response.Body())
			}
		})
	}
}
