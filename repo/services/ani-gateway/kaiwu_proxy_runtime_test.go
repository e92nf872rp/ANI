package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGatewayKaiwuProxyHTTPClientDoesNotFollowRedirect 验证代理专用
// HTTP client 保留 DSH 的 303 响应，让 Gateway 能改写 Location 和 Cookie。
func TestGatewayKaiwuProxyHTTPClientDoesNotFollowRedirect(t *testing.T) {
	redirected := false
	dsh := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/" {
			writer.Header().Set("Location", "/final")
			writer.WriteHeader(http.StatusSeeOther)
			return
		}
		redirected = true
		writer.WriteHeader(http.StatusOK)
	}))
	defer dsh.Close()

	client := newGatewayKaiwuProxyHTTPClient()
	request, err := http.NewRequest(http.MethodGet, dsh.URL+"/", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if location := response.Header.Get("Location"); location != "/final" {
		t.Fatalf("Location=%q, want /final", location)
	}
	if redirected {
		t.Fatal("proxy HTTP client followed redirect")
	}
}
