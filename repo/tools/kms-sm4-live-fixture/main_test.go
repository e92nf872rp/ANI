package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestFixtureRunsKMSAndObjectRoundTrip(t *testing.T) {
	srv, err := newServer("kms-token", t.TempDir(), "test-master-key")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	httpServer := httptest.NewServer(srv.routes())
	defer httpServer.Close()

	postJSON(t, httpServer.URL+"/v1/keys", "kms-token", map[string]string{
		"tenant_id": "tenant-a",
		"key_id":    "ekey-live",
		"name":      "live",
		"algorithm": "SM4",
	})
	seal := postJSON(t, httpServer.URL+"/v1/seal", "kms-token", map[string]string{
		"tenant_id":       "tenant-a",
		"key_id":          "ekey-live",
		"algorithm":       "SM4",
		"object_uri":      "s3://ani-live-validation/model.bin",
		"idempotency_key": "seal",
	})
	if seal["sealed_object_uri"] == "" || seal["unseal_token"] == "" {
		t.Fatalf("seal response = %#v, want sealed uri and token", seal)
	}
	plaintext := []byte("live-model-payload")
	sealed := postBytes(t, httpServer.URL+"/v1/stream/seal", "kms-token", plaintext)
	if bytes.Equal(sealed, plaintext) || len(sealed) == 0 {
		t.Fatalf("sealed content = %q, want ciphertext", sealed)
	}
	putBytes(t, httpServer.URL+"/objectstore/live/sealed.bin", sealed)
	stored := getBytes(t, httpServer.URL+"/objectstore/live/sealed.bin")
	if !bytes.Equal(stored, sealed) {
		t.Fatalf("stored content changed")
	}
	opened := postBytes(t, httpServer.URL+"/v1/stream/open", "kms-token", stored)
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened content = %q, want %q", opened, plaintext)
	}
}

func TestFixtureRejectsTraversalObjectPath(t *testing.T) {
	srv, err := newServer("", t.TempDir(), "test-master-key")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	if _, ok := srv.objectPath("../secret"); ok {
		t.Fatalf("objectPath accepted traversal")
	}
	if path, ok := srv.objectPath("tenant/object.bin"); !ok || filepath.Base(path) != "object.bin" {
		t.Fatalf("objectPath() = %q, %v", path, ok)
	}
}

func postJSON(t *testing.T, url string, token string, payload any) map[string]any {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		content, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s status = %d: %s", url, resp.StatusCode, content)
	}
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func postBytes(t *testing.T, url string, token string, content []byte) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return doBytes(t, req, http.StatusOK)
}

func putBytes(t *testing.T, url string, content []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	_ = doBytes(t, req, http.StatusNoContent)
}

func getBytes(t *testing.T, url string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return doBytes(t, req, http.StatusOK)
}

func doBytes(t *testing.T, req *http.Request, want int) []byte {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s status = %d, want %d: %s", req.Method, req.URL, resp.StatusCode, want, content)
	}
	return content
}
