package runtime

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// newKaiwuTestClient 基于进程内 fake API server 构建测试用 REST client。
func newKaiwuTestClient(t *testing.T, handler http.HandlerFunc) *KubernetesRESTClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:       server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}
	return client
}

// kaiwuServiceJSON 渲染最小可用的 Kaiwu Service 响应。
func kaiwuServiceJSON(clusterIP string, port int) string {
	return `{"spec":{"type":"ClusterIP","clusterIP":"` + clusterIP + `","ports":[{"name":"http","port":` + itoa(port) + `}]}}`
}

// kaiwuSecretJSON 渲染带可选 token 的最小 Secret 响应。
func kaiwuSecretJSON(token string, include bool) string {
	if !include {
		return `{"data":{}}`
	}
	return `{"data":{"webToken":"` + base64.StdEncoding.EncodeToString([]byte(token)) + `"}}`
}

// TestKubernetesKaiwuRuntimeReaderReturnsConfiguredResources 验证
// console/boss 会映射到配置的 Service 和 Secret 名称。
func TestKubernetesKaiwuRuntimeReaderReturnsConfiguredResources(t *testing.T) {
	var servicePaths []string
	var secretPaths []string
	client := newKaiwuTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("request = %s %s, want GET", request.Method, request.URL.Path)
		}
		switch {
		case strings.HasSuffix(request.URL.Path, "/services/console-service"):
			servicePaths = append(servicePaths, request.URL.Path)
			_, _ = writer.Write([]byte(kaiwuServiceJSON("10.96.10.10", 3080)))
		case strings.HasSuffix(request.URL.Path, "/secrets/console-secret"):
			secretPaths = append(secretPaths, request.URL.Path)
			_, _ = writer.Write([]byte(kaiwuSecretJSON("console-token", true)))
		case strings.HasSuffix(request.URL.Path, "/services/boss-service"):
			servicePaths = append(servicePaths, request.URL.Path)
			_, _ = writer.Write([]byte(kaiwuServiceJSON("10.96.10.11", 3080)))
		case strings.HasSuffix(request.URL.Path, "/secrets/boss-secret"):
			secretPaths = append(secretPaths, request.URL.Path)
			_, _ = writer.Write([]byte(kaiwuSecretJSON("boss-token", true)))
		default:
			http.NotFound(writer, request)
		}
	})
	reader := NewKubernetesKaiwuRuntimeReader(client, KubernetesKaiwuRuntimeConfig{
		Namespace:      "kw",
		ConsoleService: "console-service",
		ConsoleSecret:  "console-secret",
		BossService:    "boss-service",
		BossSecret:     "boss-secret",
	})

	target, token, err := reader.GetKaiwuRuntime(context.Background(), "console")
	if err != nil {
		t.Fatalf("console GetKaiwuRuntime() error = %v", err)
	}
	if target.String() != "http://10.96.10.10:3080" || token != "console-token" {
		t.Fatalf("console result = %q %q", target.String(), token)
	}

	target, token, err = reader.GetKaiwuRuntime(context.Background(), "boss")
	if err != nil {
		t.Fatalf("boss GetKaiwuRuntime() error = %v", err)
	}
	if target.String() != "http://10.96.10.11:3080" || token != "boss-token" {
		t.Fatalf("boss result = %q %q", target.String(), token)
	}
	if len(servicePaths) != 2 || len(secretPaths) != 2 {
		t.Fatalf("paths = %v %v", servicePaths, secretPaths)
	}
}

// TestKubernetesKaiwuRuntimeReaderRereadsSecretOnEveryCall 验证两次调用之间
// 不缓存 webToken 或 ClusterIP 数据。
func TestKubernetesKaiwuRuntimeReaderRereadsSecretOnEveryCall(t *testing.T) {
	var serviceReads atomic.Int32
	var secretReads atomic.Int32
	client := newKaiwuTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/services/kaiwu-console"):
			serviceReads.Add(1)
			_, _ = writer.Write([]byte(kaiwuServiceJSON("10.96.10.10", 3080)))
		case strings.HasSuffix(request.URL.Path, "/secrets/kaiwu-console-web-token"):
			index := secretReads.Add(1)
			token := "token-one"
			if index == 2 {
				token = "token-two"
			}
			_, _ = writer.Write([]byte(kaiwuSecretJSON(token, true)))
		default:
			http.NotFound(writer, request)
		}
	})
	reader := NewKubernetesKaiwuRuntimeReader(client, KubernetesKaiwuRuntimeConfig{})

	_, first, err := reader.GetKaiwuRuntime(context.Background(), "console")
	if err != nil {
		t.Fatalf("first GetKaiwuRuntime() error = %v", err)
	}
	_, second, err := reader.GetKaiwuRuntime(context.Background(), "console")
	if err != nil {
		t.Fatalf("second GetKaiwuRuntime() error = %v", err)
	}
	if first != "token-one" || second != "token-two" {
		t.Fatal("web token was not reread from Kubernetes")
	}
	if serviceReads.Load() != 2 || secretReads.Load() != 2 {
		t.Fatalf("reads = service %d secret %d, want 2 and 2", serviceReads.Load(), secretReads.Load())
	}
}

// TestKubernetesKaiwuRuntimeReaderRejectsInvalidInput 验证非法 client、
// Service、Secret 和 token 数据都会 fail closed。
func TestKubernetesKaiwuRuntimeReaderRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name         string
		client       string
		serviceBody  string
		secretBody   string
		serviceError bool
		secretError  bool
		wantInvalid  bool
	}{
		{name: "client", client: "unknown", serviceBody: kaiwuServiceJSON("10.96.10.10", 3080), secretBody: kaiwuSecretJSON("token", true), wantInvalid: true},
		{name: "service missing", client: "console", serviceError: true, secretBody: kaiwuSecretJSON("token", true)},
		{name: "cluster IP none", client: "console", serviceBody: kaiwuServiceJSON("None", 3080), secretBody: kaiwuSecretJSON("token", true)},
		{name: "wrong port", client: "console", serviceBody: kaiwuServiceJSON("10.96.10.10", 80), secretBody: kaiwuSecretJSON("token", true)},
		{name: "node port service", client: "console", serviceBody: `{"spec":{"type":"NodePort","clusterIP":"10.96.10.10","ports":[{"name":"http","port":3080}]}}`, secretBody: kaiwuSecretJSON("token", true)},
		{name: "secret missing", client: "console", serviceBody: kaiwuServiceJSON("10.96.10.10", 3080), secretError: true},
		{name: "token missing", client: "console", serviceBody: kaiwuServiceJSON("10.96.10.10", 3080), secretBody: kaiwuSecretJSON("", false)},
		{name: "token invalid base64", client: "console", serviceBody: kaiwuServiceJSON("10.96.10.10", 3080), secretBody: `{"data":{"webToken":"not-base64"}}`},
		{name: "token empty", client: "console", serviceBody: kaiwuServiceJSON("10.96.10.10", 3080), secretBody: kaiwuSecretJSON("", true)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newKaiwuTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				switch {
				case test.serviceError:
					http.NotFound(writer, request)
				case strings.HasSuffix(request.URL.Path, "/services/kaiwu-console"):
					_, _ = writer.Write([]byte(test.serviceBody))
				case test.secretError:
					http.NotFound(writer, request)
				default:
					_, _ = writer.Write([]byte(test.secretBody))
				}
			})
			reader := NewKubernetesKaiwuRuntimeReader(client, KubernetesKaiwuRuntimeConfig{})

			target, token, err := reader.GetKaiwuRuntime(context.Background(), test.client)
			if err == nil {
				t.Fatalf("GetKaiwuRuntime() error = nil, target=%v token=%v", target, token)
			}
			if test.wantInvalid && !errors.Is(err, ports.ErrInvalid) {
				t.Fatalf("GetKaiwuRuntime() error = %v, want ErrInvalid", err)
			}
			if !test.wantInvalid && !errors.Is(err, ports.ErrUnavailable) {
				t.Fatalf("GetKaiwuRuntime() error = %v, want ErrUnavailable", err)
			}
			if strings.Contains(err.Error(), "token-one") || strings.Contains(err.Error(), "not-base64") {
				t.Fatal("GetKaiwuRuntime() error leaked secret material")
			}
		})
	}
}
