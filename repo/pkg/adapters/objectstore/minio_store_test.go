package objectstore

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMinIOObjectStoreEnsureBucketCreatesMissingBucketWithSignedRequest(t *testing.T) {
	t.Parallel()

	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Fatalf("request missing SigV4 authorization header: %q", r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodHead {
			return minIOTestResponse(http.StatusNotFound), nil
		}
		return minIOTestResponse(http.StatusOK), nil
	})}

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint:        "http://minio.test",
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		Region:          "us-east-1",
		HTTPClient:      client,
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}

	if err := store.EnsureBucket(context.Background(), ports.BucketClass("models-a")); err != nil {
		t.Fatalf("EnsureBucket() error = %v", err)
	}

	want := []string{"HEAD /models-a", "PUT /models-a"}
	if strings.Join(requests, ",") != strings.Join(want, ",") {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestMinIOObjectStoreEnforcesRequestTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint:        "http://minio.test",
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		HTTPClient:      client,
		RequestTimeout:  time.Millisecond,
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}

	err = store.EnsureBucket(context.Background(), ports.BucketClass("models-a"))
	if err == nil {
		t.Fatal("EnsureBucket() error = nil, want request timeout")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("EnsureBucket() error = %v, want deadline exceeded", err)
	}
}

func TestMinIOObjectStoreHealthUsesSignedRootRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Fatalf("request = %s %s, want GET /", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Fatalf("request missing SigV4 authorization header: %q", r.Header.Get("Authorization"))
		}
		return minIOTestResponse(http.StatusOK), nil
	})}

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint:        "http://minio.test",
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		HTTPClient:      client,
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}

	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
}

func TestMinIOAcceptsEndpointList(t *testing.T) {
	var gotHost string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		return minIOTestResponse(http.StatusOK), nil
	})}

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoints:       []string{"http://minio-a.test:9000", "http://minio-b.test:9000"},
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		HTTPClient:      client,
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if gotHost != "minio-a.test:9000" {
		t.Fatalf("host = %q, want first endpoint minio-a.test:9000", gotHost)
	}
}

func TestMinIOHealthFailsOverEndpointList(t *testing.T) {
	var hosts []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		if r.URL.Host == "minio-a.test:9000" {
			return minIOTestResponse(http.StatusServiceUnavailable), nil
		}
		return minIOTestResponse(http.StatusOK), nil
	})}

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoints:       []string{"http://minio-a.test:9000", "http://minio-b.test:9000"},
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		HTTPClient:      client,
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	want := []string{"minio-a.test:9000", "minio-b.test:9000"}
	if strings.Join(hosts, ",") != strings.Join(want, ",") {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}
}

func TestMinIOObjectStoreEnsureBucketTreatsExistingBucketAsReady(t *testing.T) {
	t.Parallel()

	var putCalled bool
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			putCalled = true
		}
		return minIOTestResponse(http.StatusOK), nil
	})}

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint:        "http://minio.test",
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		Region:          "us-east-1",
		HTTPClient:      client,
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}

	if err := store.EnsureBucket(context.Background(), ports.BucketClass("datasets-a")); err != nil {
		t.Fatalf("EnsureBucket() error = %v", err)
	}
	if putCalled {
		t.Fatal("EnsureBucket() called PUT after HEAD returned ready")
	}
}

func TestMinIOObjectStoreBuildsTenantScopedSignedUploadAndDownloadURLs(t *testing.T) {
	t.Parallel()

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint:        "https://minio.example:9000",
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		SessionToken:    "session-token",
		Region:          "us-east-1",
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	ref := ports.ObjectRef{
		TenantID:    "tenant-a",
		BucketClass: ports.BucketClass("models-a"),
		ObjectKey:   "llm/model.bin",
	}

	upload, err := store.SignedUploadURL(context.Background(), ref, 10*time.Minute)
	if err != nil {
		t.Fatalf("SignedUploadURL() error = %v", err)
	}
	download, err := store.SignedDownloadURL(context.Background(), ref, 15*time.Minute)
	if err != nil {
		t.Fatalf("SignedDownloadURL() error = %v", err)
	}

	assertSignedURL(t, upload.URL, "https://minio.example:9000/models-a/tenant-a/llm/model.bin", "600")
	assertSignedURL(t, download.URL, "https://minio.example:9000/models-a/tenant-a/llm/model.bin", "900")
	if !upload.ExpiresAt.Equal(fixedMinIOTestClock().Add(10 * time.Minute)) {
		t.Fatalf("upload expires_at = %s", upload.ExpiresAt)
	}
	if !download.ExpiresAt.Equal(fixedMinIOTestClock().Add(15 * time.Minute)) {
		t.Fatalf("download expires_at = %s", download.ExpiresAt)
	}
}

func TestMinIOObjectStoreUsesPublicEndpointOnlyForSignedURLs(t *testing.T) {
	t.Parallel()

	var apiHosts []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		apiHosts = append(apiHosts, r.Host)
		return minIOTestResponse(http.StatusOK), nil
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint:        "http://minio.ani-s05-objectstore.svc.cluster.local:9000",
		PublicEndpoint:  "http://minio-public.example:30900",
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		SessionToken:    "session-token",
		Region:          "us-east-1",
		HTTPClient:      client,
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	if err := store.EnsureBucket(context.Background(), ports.BucketClass("models-a")); err != nil {
		t.Fatalf("EnsureBucket() error = %v", err)
	}
	if len(apiHosts) != 1 || apiHosts[0] != "minio.ani-s05-objectstore.svc.cluster.local:9000" {
		t.Fatalf("api hosts = %v, want internal endpoint", apiHosts)
	}

	upload, err := store.SignedUploadURL(context.Background(), ports.ObjectRef{
		TenantID:    "tenant-a",
		BucketClass: ports.BucketClass("models-a"),
		ObjectKey:   "live.txt",
	}, time.Minute)
	if err != nil {
		t.Fatalf("SignedUploadURL() error = %v", err)
	}
	assertSignedURL(t, upload.URL, "http://minio-public.example:30900/models-a/tenant-a/live.txt", "60")
}

func TestMinIOObjectStoreRejectsInvalidPresignInput(t *testing.T) {
	t.Parallel()

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint:        "http://minio.example:9000",
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		Region:          "us-east-1",
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}

	_, err = store.SignedUploadURL(context.Background(), ports.ObjectRef{
		TenantID:    "tenant-a",
		BucketClass: ports.BucketClass("models-a"),
	}, time.Minute)
	if err == nil {
		t.Fatal("SignedUploadURL() error = nil, want invalid request error")
	}
}

func assertSignedURL(t *testing.T, rawURL string, wantPrefix string, wantExpires string) {
	t.Helper()

	if !strings.HasPrefix(rawURL, wantPrefix+"?") {
		t.Fatalf("signed URL = %q, want prefix %q", rawURL, wantPrefix+"?")
	}
	for _, token := range []string{
		"X-Amz-Algorithm=AWS4-HMAC-SHA256",
		"X-Amz-Credential=minio%2F20260619%2Fus-east-1%2Fs3%2Faws4_request",
		"X-Amz-Date=20260619T010203Z",
		"X-Amz-Security-Token=session-token",
		"X-Amz-SignedHeaders=host",
		"X-Amz-Signature=",
		"X-Amz-Expires=" + wantExpires,
	} {
		if !strings.Contains(rawURL, token) {
			t.Fatalf("signed URL %q missing %q", rawURL, token)
		}
	}
}

func fixedMinIOTestClock() time.Time {
	return time.Date(2026, 6, 19, 1, 2, 3, 0, time.UTC)
}

func TestMinIOObjectStoreBucketUsageAggregatesTenantScopedListing(t *testing.T) {
	pageOne := `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <IsTruncated>true</IsTruncated>
  <NextContinuationToken>token-2</NextContinuationToken>
  <Contents><Key>tenant-a/raw/a.csv</Key><Size>100</Size></Contents>
  <Contents><Key>tenant-a/raw/b.csv</Key><Size>200</Size></Contents>
</ListBucketResult>`
	pageTwo := `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>tenant-a/raw/c.csv</Key><Size>52419</Size></Contents>
</ListBucketResult>`

	var queries []string
	page := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/ani-s13-test2" {
			t.Fatalf("request = %s %s, want GET /ani-s13-test2", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Fatalf("request missing SigV4 authorization header: %q", r.Header.Get("Authorization"))
		}
		query := r.URL.Query()
		queries = append(queries, query.Get("list-type")+"|"+query.Get("prefix")+"|"+query.Get("continuation-token"))
		if query.Get("list-type") != "2" || query.Get("prefix") != "tenant-a/" {
			t.Fatalf("list query = %v, want list-type=2 prefix=tenant-a/", r.URL.RawQuery)
		}
		page++
		if page == 1 {
			if query.Get("continuation-token") != "" {
				t.Fatalf("first page continuation-token = %q, want empty", query.Get("continuation-token"))
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(pageOne))}, nil
		}
		if query.Get("continuation-token") != "token-2" {
			t.Fatalf("second page continuation-token = %q, want token-2", query.Get("continuation-token"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(pageTwo))}, nil
	})}

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint:        "http://minio.test",
		AccessKeyID:     "minio",
		SecretAccessKey: "secret",
		BucketPrefix:    "ani-s13-",
		HTTPClient:      client,
		Now:             fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}

	usage, err := store.BucketUsage(context.Background(), ports.BucketClass("test2"), "tenant-a")
	if err != nil {
		t.Fatalf("BucketUsage() error = %v", err)
	}
	if usage.ObjectCount != 3 || usage.SizeBytes != 52719 {
		t.Fatalf("usage = %#v, want 3 objects totaling 52719 bytes", usage)
	}
	if len(queries) != 2 {
		t.Fatalf("list requests = %v, want two paginated requests", queries)
	}

	if _, err := store.BucketUsage(context.Background(), ports.BucketClass("test2"), ""); err == nil {
		t.Fatal("BucketUsage() with empty tenant error = nil, want invalid")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func minIOTestResponse(statusCode int) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}
