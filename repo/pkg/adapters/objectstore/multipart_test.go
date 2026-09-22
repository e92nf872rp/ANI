package objectstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMinIOMultipartFlowUsesTenantPrefix(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>model</Bucket><Key>tenant-a/models/a.bin</Key><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodGet && r.URL.Query().Get("uploadId") == "upload-1":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<ListPartsResult><Bucket>model</Bucket><Key>tenant-a/models/a.bin</Key><UploadId>upload-1</UploadId><PartNumberMarker>0</PartNumberMarker><NextPartNumberMarker>1</NextPartNumberMarker><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>&#34;etag-1&#34;</ETag><Size>3</Size></Part></ListPartsResult>`)
		case r.Method == http.MethodPut && r.URL.Query().Get("partNumber") == "1":
			w.Header().Set("ETag", `"etag-1"`)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "upload-1":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><Bucket>model</Bucket><Key>tenant-a/models/a.bin</Key><ETag>&#34;complete-etag&#34;</ETag></CompleteMultipartUploadResult>`)
		case r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") == "upload-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: server.URL, AccessKeyID: "access", SecretAccessKey: "secret", RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ref := ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClassModel, ObjectKey: "models/a.bin"}
	uploadID, err := store.BeginMultipart(context.Background(), ref, "application/octet-stream")
	if err != nil || uploadID != "upload-1" {
		t.Fatalf("BeginMultipart() = %q, %v", uploadID, err)
	}
	gotParts, err := store.ListParts(context.Background(), ref, uploadID)
	if err != nil || len(gotParts) != 1 || gotParts[0].ETag != "etag-1" {
		t.Fatalf("ListParts() = %#v, %v", gotParts, err)
	}
	part, err := store.UploadPart(context.Background(), ref, uploadID, 1, strings.NewReader("abc"), 3)
	if err != nil || part.ETag != "etag-1" || part.Size != 3 {
		t.Fatalf("UploadPart() = %#v, %v", part, err)
	}
	metadata, err := store.CompleteMultipart(context.Background(), ref, uploadID, []ports.MultipartPart{part})
	if err != nil || metadata.SizeBytes != 3 || metadata.Checksum != "" || metadata.Ref != ref {
		t.Fatalf("CompleteMultipart() = %#v, %v", metadata, err)
	}
	if err := store.AbortMultipart(context.Background(), ref, uploadID); err != nil {
		t.Fatalf("AbortMultipart() error = %v", err)
	}
	mu.Lock()
	joined := strings.Join(paths, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "/model/tenant-a/models/a.bin") {
		t.Fatalf("requests did not preserve tenant prefix: %s", joined)
	}
}

func TestMinIOMultipartGuardsPartLengthsAndCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.Header().Set("ETag", `"etag"`)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: server.URL, AccessKeyID: "access", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	ref := ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClassModel, ObjectKey: "x"}
	if _, err := store.UploadPart(context.Background(), ref, "upload", 1, strings.NewReader("ab"), 3); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("short UploadPart() error = %v, want ErrInvalid", err)
	}
	if _, err := store.UploadPart(context.Background(), ref, "upload", 1, strings.NewReader("abcd"), 3); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("long UploadPart() error = %v, want ErrInvalid", err)
	}
	if _, err := store.CompleteMultipart(context.Background(), ref, "upload", nil); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("empty CompleteMultipart() error = %v, want ErrInvalid", err)
	}
}

func TestMinIOMultipartPreservesContextDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: server.URL, AccessKeyID: "access", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err = store.BeginMultipart(ctx, ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClassModel, ObjectKey: "x"}, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("BeginMultipart() error = %v, want deadline exceeded", err)
	}
}

func TestMinIOMultipartSanitizesProviderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret provider detail", http.StatusInternalServerError)
	}))
	defer server.Close()
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{Endpoint: server.URL, AccessKeyID: "access", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.BeginMultipart(context.Background(), ports.ObjectRef{TenantID: "tenant-a", BucketClass: ports.BucketClassModel, ObjectKey: "x"}, "")
	if err == nil || strings.Contains(err.Error(), "secret provider detail") || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("BeginMultipart() error = %v, want sanitized HTTP 500", err)
	}
}
