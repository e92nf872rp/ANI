package router

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestCreateImageUploadRewritesUploadURLToGatewayProxy(t *testing.T) {
	service := &fakeImageImportService{
		session: ports.ImageUploadSession{
			Image: ports.ImageRecord{
				ID:        "img-1",
				TenantID:  "tenant-a",
				Name:      "ubuntu",
				Format:    ports.ImageFormatISO,
				SizeGiB:   5,
				State:     ports.ImageStatePending,
				CreatedAt: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
			},
			UploadURL: "https://192.168.102.51:31001/v1beta1/upload",
			Token:     "tok",
			ExpiresAt: time.Date(2026, 7, 10, 0, 15, 0, 0, time.UTC),
			Method:    "POST",
		},
	}
	h := server.Default()
	v1 := h.Group("/api/v1")
	registerImageResourcesWithService(v1, service, withImagePublicBaseURL("http://192.168.102.51:30080"))

	body := `{"idempotency_key":"k1","name":"ubuntu","format":"iso","size_gib":5}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/images/uploads", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "11111111-1111-1111-1111-111111111111"},
	).Result()
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if !strings.Contains(string(resp.Body()), `"upload_url":"http://192.168.102.51:30080/api/v1/images/upload-proxy"`) {
		t.Fatalf("body = %s, want gateway upload-proxy url", resp.Body())
	}
}

func TestProxyImageUploadForwardsToCDI(t *testing.T) {
	var gotAuth, gotCT string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		if r.URL.Path != "/v1beta1/upload" {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	api := newImageAPIWithService(&fakeImageImportService{})
	api.uploadProxyURL = upstream.URL
	api.uploadHTTPClient = upstream.Client()

	h := server.Default()
	v1 := h.Group("/api/v1")
	v1.POST("/images/upload-proxy", api.proxyImageUpload)

	payload := []byte("ISO-BYTES")
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/images/upload-proxy", &ut.Body{Body: bytes.NewReader(payload), Len: len(payload)},
		ut.Header{Key: "Authorization", Value: "Bearer upload-tok"},
		ut.Header{Key: "Content-Type", Value: "application/octet-stream"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if gotAuth != "Bearer upload-tok" {
		t.Fatalf("upstream auth = %q", gotAuth)
	}
	if gotCT != "application/octet-stream" {
		t.Fatalf("upstream content-type = %q", gotCT)
	}
	if string(gotBody) != "ISO-BYTES" {
		t.Fatalf("upstream body = %q", gotBody)
	}
}

func TestProxyImageUploadOptionsCORS(t *testing.T) {
	api := newImageAPIWithService(&fakeImageImportService{})
	h := server.Default()
	v1 := h.Group("/api/v1")
	v1.OPTIONS("/images/upload-proxy", api.proxyImageUpload)
	resp := ut.PerformRequest(h.Engine, http.MethodOptions, "/api/v1/images/upload-proxy", nil).Result()
	if resp.StatusCode() != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode())
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("cors origin = %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}
}
