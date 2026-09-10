package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

func TestMinIOPutObjectStreamsBodyAndSignsTrustedSHA256Metadata(t *testing.T) {
	content := bytes.Repeat([]byte("m"), 256*1024)
	digest := sha256.Sum256(content)
	checksum := hex.EncodeToString(digest[:])
	reader := &gatedReadReader{data: content, ready: make(chan struct{})}
	var gotBody []byte
	var gotMetadata string
	var gotAuthorization string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(reader.ready)
		if r.ContentLength != int64(len(content)) {
			t.Fatalf("content length = %d, want %d", r.ContentLength, len(content))
		}
		gotMetadata = r.Header.Get("x-amz-meta-sha256")
		gotAuthorization = r.Header.Get("Authorization")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		return minIOTestResponse(http.StatusOK), nil
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint: "http://minio.test", AccessKeyID: "minio", SecretAccessKey: "secret", Region: "us-east-1", HTTPClient: client, Now: fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	metadata, err := store.PutObject(context.Background(), ports.PutObjectInput{
		Ref:  ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("model"), ObjectKey: "import/model.tar.gz"},
		Body: reader, SizeBytes: int64(len(content)), ContentType: "application/gzip", Checksum: "sha256:" + checksum,
	})
	if err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}
	if !bytes.Equal(gotBody, content) {
		t.Fatalf("uploaded body differs: got %d bytes, want %d", len(gotBody), len(content))
	}
	if gotMetadata != checksum {
		t.Fatalf("x-amz-meta-sha256 = %q, want bare canonical checksum", gotMetadata)
	}
	if !strings.Contains(gotAuthorization, "x-amz-meta-sha256") {
		t.Fatalf("Authorization = %q, want signed checksum metadata", gotAuthorization)
	}
	if metadata.SizeBytes != int64(len(content)) || metadata.Checksum != "sha256:"+checksum {
		t.Fatalf("metadata = %#v, want size/checksum from input", metadata)
	}
}

func TestMinIOPutObjectRejectsBodyWhenDeclaredSHA256IsForged(t *testing.T) {
	content := []byte("right")
	digest := sha256.Sum256(content)
	deleted := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodPut:
			if _, err := io.ReadAll(r.Body); err != nil {
				return nil, err
			}
			return minIOTestResponse(http.StatusOK), nil
		case http.MethodDelete:
			deleted = true
			return minIOTestResponse(http.StatusOK), nil
		default:
			return minIOTestResponse(http.StatusNotFound), nil
		}
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: "http://minio.test", AccessKeyID: "minio", SecretAccessKey: "secret", HTTPClient: client, Now: fixedMinIOTestClock})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	_, err = store.PutObject(context.Background(), ports.PutObjectInput{
		Ref:  ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("model"), ObjectKey: "forged.bin"},
		Body: bytes.NewReader([]byte("wrong")), SizeBytes: int64(len("wrong")), Checksum: "sha256:" + hex.EncodeToString(digest[:]),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("PutObject() error = %v, want checksum mismatch", err)
	}
	if !deleted {
		t.Fatal("forged object was not scheduled for cleanup")
	}
}

func TestMinIOVerifyObjectRejectsPresignedPayloadWithForgedMetadata(t *testing.T) {
	content := []byte("right")
	digest := sha256.Sum256(content)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			return minIOTestResponse(http.StatusNotFound), nil
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: int64(len("wrong")),
			Header: http.Header{
				"Content-Length":    []string{fmt.Sprint(len("wrong"))},
				"X-Amz-Meta-Sha256": []string{"sha256:" + hex.EncodeToString(digest[:])},
			},
			Body: io.NopCloser(strings.NewReader("wrong")),
		}, nil
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: "http://minio.test", AccessKeyID: "minio", SecretAccessKey: "secret", HTTPClient: client, Now: fixedMinIOTestClock})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	err = store.VerifyObject(context.Background(), ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("model"), ObjectKey: "presigned.bin"}, int64(len(content)), "sha256:"+hex.EncodeToString(digest[:]))
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("VerifyObject() error = %v, want checksum mismatch", err)
	}
}

func TestMinIOPutObjectDoesNotRetryNonReplayableBody(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		_, _ = io.Copy(io.Discard, r.Body)
		return minIOTestResponse(http.StatusServiceUnavailable), nil
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoints: []string{"http://minio-a.test", "http://minio-b.test"}, AccessKeyID: "minio", SecretAccessKey: "secret", Region: "us-east-1", HTTPClient: client, Now: fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	_, err = store.PutObject(context.Background(), ports.PutObjectInput{
		Ref:  ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("model"), ObjectKey: "model.bin"},
		Body: strings.NewReader("model"), SizeBytes: int64(len("model")), Checksum: "sha256:" + strings.Repeat("ab", 32),
	})
	if err == nil {
		t.Fatal("PutObject() error = nil, want service unavailable")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one attempt for non-replayable body", requests)
	}
}

func TestMinIOPutObjectRejectsInvalidChecksumBeforeRequest(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return minIOTestResponse(http.StatusOK), nil
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: "http://minio.test", AccessKeyID: "minio", SecretAccessKey: "secret", HTTPClient: client, Now: fixedMinIOTestClock})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	_, err = store.PutObject(context.Background(), ports.PutObjectInput{
		Ref: ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("model"), ObjectKey: "model.bin"}, Body: strings.NewReader("model"), SizeBytes: 5, Checksum: "md5:bad",
	})
	if err == nil {
		t.Fatal("PutObject() error = nil, want invalid checksum")
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want no request for invalid checksum", requests)
	}
}

func TestMinIOPutObjectRejectsShortBodyAfterStreaming(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		return minIOTestResponse(http.StatusOK), nil
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: "http://minio.test", AccessKeyID: "minio", SecretAccessKey: "secret", HTTPClient: client, Now: fixedMinIOTestClock})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	_, err = store.PutObject(context.Background(), ports.PutObjectInput{
		Ref: ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("model"), ObjectKey: "model.bin"}, Body: strings.NewReader("tiny"), SizeBytes: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("PutObject() error = %v, want body size mismatch", err)
	}
}

func TestMinIOPutObjectPreservesUnknownSizeStreaming(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.ContentLength != 0 {
			t.Fatalf("content length = %d, want unknown length", r.ContentLength)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if string(body) != "legacy-stream" {
			t.Fatalf("body = %q", body)
		}
		return minIOTestResponse(http.StatusOK), nil
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: "http://minio.test", AccessKeyID: "minio", SecretAccessKey: "secret", HTTPClient: client, Now: fixedMinIOTestClock})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	metadata, err := store.PutObject(context.Background(), ports.PutObjectInput{
		Ref: ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("model"), ObjectKey: "legacy.bin"}, Body: strings.NewReader("legacy-stream"),
	})
	if err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}
	if metadata.SizeBytes != int64(len("legacy-stream")) {
		t.Fatalf("metadata size = %d, want %d", metadata.SizeBytes, len("legacy-stream"))
	}
}

type gatedReadReader struct {
	data     []byte
	position int
	ready    chan struct{}
}

func (r *gatedReadReader) Read(p []byte) (int, error) {
	select {
	case <-r.ready:
	default:
		return 0, fmt.Errorf("body consumed before transport started")
	}
	if r.position >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.position:])
	r.position += n
	return n, nil
}

func TestMinIOObjectStoreSignsChecksumUploadHeader(t *testing.T) {
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint: "https://minio.example:9000", AccessKeyID: "minio", SecretAccessKey: "secret", Region: "us-east-1", Now: fixedMinIOTestClock,
	})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	checksum := "sha256:" + strings.Repeat("ab", 32)
	signed, err := store.SignedUploadURLWithHeaders(context.Background(), ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("models-a"), ObjectKey: "model.bin"}, time.Minute, map[string]string{"x-amz-meta-sha256": checksum})
	if err != nil {
		t.Fatalf("SignedUploadURLWithHeaders() error = %v", err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	if got := parsed.Query().Get("X-Amz-SignedHeaders"); got != "host;x-amz-meta-sha256" {
		t.Fatalf("signed headers = %q, want host;x-amz-meta-sha256", got)
	}
	if signed.Headers["x-amz-meta-sha256"] != checksum {
		t.Fatalf("returned headers = %#v, want checksum metadata", signed.Headers)
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

func TestMetadataFromResponseUsesS3SHA256Checksum(t *testing.T) {
	store := &MinIOObjectStore{now: fixedMinIOTestClock}
	response := minIOTestResponse(http.StatusOK)
	response.ContentLength = 37
	response.Header.Set("X-Amz-Checksum-Sha256", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	response.Header.Set("ETag", "\"etag-md5\"")

	metadata := store.metadataFromResponse(ports.ObjectRef{BucketClass: "model"}, response)
	if metadata.Checksum == "etag-md5" {
		t.Fatalf("checksum used ETag instead of S3 SHA-256 checksum: %#v", metadata)
	}
	if metadata.Checksum != strings.Repeat("00", 32) {
		t.Fatalf("checksum = %q, want hex SHA-256 bytes", metadata.Checksum)
	}
}

func TestMetadataFromResponseUsesModelUploadSHA256Metadata(t *testing.T) {
	store := &MinIOObjectStore{now: fixedMinIOTestClock}
	response := minIOTestResponse(http.StatusOK)
	response.ContentLength = 37
	response.Header.Set("X-Amz-Meta-Sha256", "sha256:"+strings.Repeat("ab", 32))
	response.Header.Set("ETag", "\"0123456789abcdef0123456789abcdef\"")
	metadata := store.metadataFromResponse(ports.ObjectRef{BucketClass: "model"}, response)
	if metadata.Checksum != strings.Repeat("ab", 32) {
		t.Fatalf("checksum = %q, want model upload SHA-256 metadata", metadata.Checksum)
	}
}

func TestMetadataFromResponseDoesNotFallbackToETagWhenSHA256HeaderIsInvalid(t *testing.T) {
	store := &MinIOObjectStore{now: fixedMinIOTestClock}
	response := minIOTestResponse(http.StatusOK)
	response.Header.Set("X-Amz-Meta-Sha256", "not-a-digest")
	response.Header.Set("ETag", "\"0123456789abcdef0123456789abcdef\"")
	metadata := store.metadataFromResponse(ports.ObjectRef{BucketClass: "model"}, response)
	if metadata.Checksum != "" {
		t.Fatalf("checksum = %q, want empty rather than untrusted ETag fallback", metadata.Checksum)
	}
}

func TestMetadataFromResponseFallsBackToETagWithoutChecksumHeader(t *testing.T) {
	store := &MinIOObjectStore{now: fixedMinIOTestClock}
	response := minIOTestResponse(http.StatusOK)
	response.Header.Set("ETag", "\"etag-md5\"")
	metadata := store.metadataFromResponse(ports.ObjectRef{BucketClass: "model"}, response)
	if metadata.Checksum != "etag-md5" {
		t.Fatalf("checksum = %q, want ETag fallback", metadata.Checksum)
	}
}

func TestStatObjectRequestsS3ChecksumMetadata(t *testing.T) {
	var checksumMode string
	var authorization string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		checksumMode = r.Header.Get("x-amz-checksum-mode")
		authorization = r.Header.Get("Authorization")
		response := minIOTestResponse(http.StatusOK)
		response.ContentLength = 37
		response.Header.Set("ETag", "\"etag-md5\"")
		return response, nil
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
	if _, err := store.StatObject(context.Background(), ports.ObjectRef{TenantID: "tenant-a", BucketClass: "model", ObjectKey: "model.bin"}); err != nil {
		t.Fatalf("StatObject() error = %v", err)
	}
	if checksumMode != "ENABLED" {
		t.Fatalf("x-amz-checksum-mode = %q, want ENABLED", checksumMode)
	}
	if !strings.Contains(authorization, "SignedHeaders=host;x-amz-checksum-mode;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("authorization = %q, want checksum-mode in signed headers", authorization)
	}
}

func TestStatObjectDoesNotTreat64HexETagAsSHA256(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := minIOTestResponse(http.StatusOK)
		response.ContentLength = 12
		response.Header.Set("ETag", `"`+strings.Repeat("ab", 32)+`"`)
		return response, nil
	})}
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: "http://minio.test", AccessKeyID: "minio", SecretAccessKey: "secret", Region: "us-east-1", HTTPClient: client, Now: fixedMinIOTestClock})
	if err != nil {
		t.Fatalf("NewMinIOObjectStore() error = %v", err)
	}
	metadata, err := store.StatObject(context.Background(), ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClass("model"), ObjectKey: "model.bin"})
	if err != nil {
		t.Fatalf("StatObject() error = %v", err)
	}
	if metadata.Checksum != "" {
		t.Fatalf("checksum = %q, want empty without authoritative SHA-256 header", metadata.Checksum)
	}
}
