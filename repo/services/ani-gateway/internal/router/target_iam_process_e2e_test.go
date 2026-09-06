//go:build integration

package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	iamadapter "github.com/kubercloud/ani/pkg/adapters/iam"
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type processE2ETargetClient struct {
	delegate      ports.TargetIAM
	passwordCalls atomic.Int32
	decisionCalls atomic.Int32
}

func (c *processE2ETargetClient) PasswordLogin(ctx context.Context, request ports.TargetIAMPasswordLoginRequest) (ports.TargetIAMPasswordLoginResult, error) {
	c.passwordCalls.Add(1)
	return c.delegate.PasswordLogin(ctx, request)
}

func (c *processE2ETargetClient) CheckPermission(ctx context.Context, request ports.TargetIAMCheckPermissionRequest) (ports.TargetIAMAuthorizationDecision, error) {
	c.decisionCalls.Add(1)
	return c.delegate.CheckPermission(ctx, request)
}

type processE2ETrustedHeaders struct {
	mu          sync.Mutex
	tenantID    string
	principalID string
	evil        string
}

func TestTargetIAMProcessE2EChild(t *testing.T) {
	if os.Getenv("DP2_GATEWAY_E2E_CHILD") != "1" {
		t.Skip("run only from the ani-iam DP2-05 process orchestrator")
	}
	t.Setenv("ANI_AUTH_MODE", "auth_service")

	client, closeClient, err := iamadapter.Dial(
		requiredProcessE2EEnv(t, "DP2_E2E_IAM_ADDR"),
		processE2EMutualTLSConfig(t),
	)
	if err != nil {
		t.Fatalf("dial real IAM process: %v", err)
	}
	t.Cleanup(func() { _ = closeClient() })
	counted := &processE2ETargetClient{delegate: client}

	store, closeStore, err := bootstrap.ConnectRedisCacheStore(requiredProcessE2EEnv(t, "DP2_E2E_REDIS_URL"))
	if err != nil {
		t.Fatalf("connect real Gateway Redis store: %v", err)
	}
	t.Cleanup(func() { _ = closeStore() })

	address := reserveProcessE2ELoopbackAddress(t)
	h := server.New(server.WithHostPorts(address), server.WithExitWaitTime(1))
	if err := middleware.RegisterWithTargetIAM(h, store, counted); err != nil {
		t.Fatalf("register production Gateway middleware: %v", err)
	}
	observed := &processE2ETrustedHeaders{}
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		if string(c.Method()) == http.MethodGet && string(c.Path()) == "/api/v1/instances" {
			observed.mu.Lock()
			observed.tenantID = string(c.GetHeader("x-ani-tenant-id"))
			observed.principalID = string(c.GetHeader("x-ani-principal-id"))
			observed.evil = string(c.GetHeader("x-ani-evil"))
			observed.mu.Unlock()
		}
		c.Next(ctx)
	})
	RegisterWithOptions(h, RegisterOptions{TargetIAMClient: counted})
	go func() { _ = h.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	})
	waitForProcessE2EHTTP(t, "http://"+address+"/healthz")

	httpClient := &http.Client{Timeout: 5 * time.Second}
	public := processE2ERequest(t, httpClient, http.MethodGet, "http://"+address+"/api/v1/branding", nil, nil)
	if public.StatusCode != http.StatusOK || counted.decisionCalls.Load() != 0 {
		t.Fatalf("public operation status/decision calls = %d/%d", public.StatusCode, counted.decisionCalls.Load())
	}
	_ = public.Body.Close()

	loginURL := "http://" + address + "/api/v1/auth/password/login"
	invalidBody := []byte(`{"account":"user@example.com","password":"wrong-password","audience":"console","boundary":{"type":"tenant","tenant_id":"0198f062-b76d-7f2a-b0ad-50a417bf1f70"}}`)
	invalid := processE2ERequest(t, httpClient, http.MethodPost, loginURL, invalidBody, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "process-invalid-login",
	})
	invalidDocument := decodeProcessE2EJSON(t, invalid)
	if invalid.StatusCode != http.StatusUnauthorized || invalidDocument["code"] != "CREDENTIAL_INVALID" {
		t.Fatalf("invalid login status/body = %d/%#v", invalid.StatusCode, invalidDocument)
	}

	loginBody := []byte(`{"account":" User@Example.COM ","password":"correct-password","audience":"console","boundary":{"type":"tenant","tenant_id":"0198f062-b76d-7f2a-b0ad-50a417bf1f70"},"device_name":"process-e2e"}`)
	login := processE2ERequest(t, httpClient, http.MethodPost, loginURL, loginBody, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "process-valid-login",
	})
	loginDocument := decodeProcessE2EJSON(t, login)
	accessToken, _ := loginDocument["access_token"].(string)
	if login.StatusCode != http.StatusOK || accessToken == "" || strings.Contains(fmt.Sprint(loginDocument), "refresh") {
		t.Fatalf("valid login status/body = %d/%#v", login.StatusCode, loginDocument)
	}
	if cookie := login.Header.Get("Set-Cookie"); !strings.Contains(cookie, "ani_console_refresh=") || !strings.Contains(strings.ToLower(cookie), "httponly") || !strings.Contains(strings.ToLower(cookie), "secure") {
		t.Fatalf("refresh cookie = %q", cookie)
	}

	protected := processE2ERequest(t, httpClient, http.MethodGet, "http://"+address+"/api/v1/instances", nil, map[string]string{
		"Authorization":      "Bearer " + accessToken,
		"X-Ani-Tenant-ID":    "attacker-tenant",
		"X-Ani-Principal-ID": "attacker-principal",
		"X-Ani-Evil":         "attacker-value",
	})
	protectedBody, err := io.ReadAll(protected.Body)
	_ = protected.Body.Close()
	if err != nil {
		t.Fatalf("read protected operation response: %v", err)
	}
	if protected.StatusCode != http.StatusOK || counted.decisionCalls.Load() != 1 {
		t.Fatalf("protected operation status/decision calls/body = %d/%d/%s", protected.StatusCode, counted.decisionCalls.Load(), protectedBody)
	}
	observed.mu.Lock()
	trustedTenant := observed.tenantID
	trustedPrincipal := observed.principalID
	evil := observed.evil
	observed.mu.Unlock()
	if trustedTenant != "0198f062-b76d-7f2a-b0ad-50a417bf1f70" || trustedPrincipal == "" || evil != "" || strings.Contains(string(protectedBody), "attacker-") {
		t.Fatalf("trusted downstream context = tenant:%q principal:%q evil:%q body:%s", trustedTenant, trustedPrincipal, evil, protectedBody)
	}
	if counted.passwordCalls.Load() != 2 {
		t.Fatalf("PasswordLogin calls = %d, want invalid plus valid", counted.passwordCalls.Load())
	}

	deniedLoginBody := []byte(`{"account":"denied@example.com","password":"correct-password","audience":"console","boundary":{"type":"tenant","tenant_id":"0198f062-b76d-7f2a-b0ad-50a417bf1f70"},"device_name":"process-e2e-denied"}`)
	deniedLogin := processE2ERequest(t, httpClient, http.MethodPost, loginURL, deniedLoginBody, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "process-denied-login",
	})
	deniedLoginDocument := decodeProcessE2EJSON(t, deniedLogin)
	deniedAccessToken, _ := deniedLoginDocument["access_token"].(string)
	if deniedLogin.StatusCode != http.StatusOK || deniedAccessToken == "" {
		t.Fatalf("denied-principal login status/body = %d/%#v", deniedLogin.StatusCode, deniedLoginDocument)
	}
	denied := processE2ERequest(t, httpClient, http.MethodGet, "http://"+address+"/api/v1/instances", nil, map[string]string{
		"Authorization": "Bearer " + deniedAccessToken,
	})
	deniedDocument := decodeProcessE2EJSON(t, denied)
	if denied.StatusCode != http.StatusForbidden || deniedDocument["code"] != "PERMISSION_DENIED" || counted.decisionCalls.Load() != 2 {
		t.Fatalf("real deny status/body/decision calls = %d/%#v/%d", denied.StatusCode, deniedDocument, counted.decisionCalls.Load())
	}

	unavailableAddress := reserveProcessE2ELoopbackAddress(t)
	unavailableClient, closeUnavailableClient, err := iamadapter.Dial(unavailableAddress, processE2EMutualTLSConfig(t))
	if err != nil {
		t.Fatalf("dial unavailable IAM network seam: %v", err)
	}
	t.Cleanup(func() { _ = closeUnavailableClient() })
	unavailableGatewayURL := startProcessE2EGateway(t, store, unavailableClient)
	unavailable := processE2ERequest(t, httpClient, http.MethodGet, unavailableGatewayURL+"/api/v1/instances", nil, map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	unavailableDocument := decodeProcessE2EJSON(t, unavailable)
	if unavailable.StatusCode != http.StatusServiceUnavailable || unavailableDocument["code"] != "IAM_UNAVAILABLE" {
		t.Fatalf("real unavailable IAM status/body = %d/%#v", unavailable.StatusCode, unavailableDocument)
	}
	t.Log("DP2_GATEWAY_PROCESS_E2E_UNAVAILABLE_PASS")

	timeoutAddress := startProcessE2EBlackhole(t)
	timeoutClient, closeTimeoutClient, err := iamadapter.Dial(timeoutAddress, processE2EMutualTLSConfig(t))
	if err != nil {
		t.Fatalf("dial timeout IAM network seam: %v", err)
	}
	t.Cleanup(func() { _ = closeTimeoutClient() })
	timeoutGatewayURL := startProcessE2EGateway(t, store, timeoutClient)
	timedOut := processE2ERequest(t, httpClient, http.MethodGet, timeoutGatewayURL+"/api/v1/instances", nil, map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	timedOutDocument := decodeProcessE2EJSON(t, timedOut)
	if timedOut.StatusCode != http.StatusGatewayTimeout || timedOutDocument["code"] != "IAM_TIMEOUT" {
		t.Fatalf("real timeout IAM status/body = %d/%#v", timedOut.StatusCode, timedOutDocument)
	}
	t.Log("DP2_GATEWAY_PROCESS_E2E_TIMEOUT_PASS")

	t.Log("DP2_GATEWAY_PROCESS_E2E_PASS")
}

func processE2EMutualTLSConfig(t *testing.T) iamadapter.MutualTLSConfig {
	t.Helper()
	return iamadapter.MutualTLSConfig{
		ServerName:      requiredProcessE2EEnv(t, "DP2_E2E_IAM_SERVER_NAME"),
		CAFile:          requiredProcessE2EEnv(t, "DP2_E2E_IAM_CA_FILE"),
		CertificateFile: requiredProcessE2EEnv(t, "DP2_E2E_GATEWAY_CERT_FILE"),
		PrivateKeyFile:  requiredProcessE2EEnv(t, "DP2_E2E_GATEWAY_KEY_FILE"),
	}
}

func startProcessE2EGateway(t *testing.T, store middleware.GatewayStore, client ports.TargetIAM) string {
	t.Helper()
	address := reserveProcessE2ELoopbackAddress(t)
	h := server.New(server.WithHostPorts(address), server.WithExitWaitTime(1))
	if err := middleware.RegisterWithTargetIAM(h, store, client); err != nil {
		t.Fatalf("register production Gateway middleware: %v", err)
	}
	RegisterWithOptions(h, RegisterOptions{TargetIAMClient: client})
	go func() { _ = h.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	})
	waitForProcessE2EHTTP(t, "http://"+address+"/healthz")
	return "http://" + address
}

func requiredProcessE2EEnv(t *testing.T, key string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Fatalf("%s is required", key)
	}
	return value
}

func reserveProcessE2ELoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve Gateway address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release Gateway address: %v", err)
	}
	return address
}

func startProcessE2EBlackhole(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for timeout IAM network seam: %v", err)
	}
	address := listener.Addr().String()

	var connectionsMu sync.Mutex
	connections := make([]net.Conn, 0, 1)
	stopped := make(chan struct{})
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				select {
				case <-stopped:
					return
				default:
					return
				}
			}
			connectionsMu.Lock()
			connections = append(connections, connection)
			connectionsMu.Unlock()
		}
	}()

	t.Cleanup(func() {
		close(stopped)
		_ = listener.Close()
		connectionsMu.Lock()
		defer connectionsMu.Unlock()
		for _, connection := range connections {
			_ = connection.Close()
		}
	})
	return address
}

func waitForProcessE2EHTTP(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Gateway HTTP listener did not become ready: %s", url)
}

func processE2ERequest(t *testing.T, client *http.Client, method, url string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return response
}

func decodeProcessE2EJSON(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	var document map[string]any
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatalf("decode HTTP %d response: %v", response.StatusCode, err)
	}
	return document
}
