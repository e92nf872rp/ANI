package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsModelArchiveObject(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
		want bool
	}{
		{name: "import archive", ref: "object://models/tenant/model/import-id/archive/model.tar.gz", want: true},
		{name: "ordinary tar suffix is not import archive", ref: "object://models/tenant/model/v1/model.tar.gz", want: false},
		{name: "ordinary object", ref: "object://models/tenant/model/v1/model.safetensors", want: false},
		{name: "query is not marker", ref: "object://models/tenant/model/v1/model.tar.gz?download=1", want: false},
		{name: "wrong scheme", ref: "https://models/tenant/model/v1/model.tar.gz", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isModelArchiveObject(tc.ref); got != tc.want {
				t.Fatalf("isModelArchiveObject(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

func TestNormalizeExtractionLimitsStayWithinImportWorkspaceBound(t *testing.T) {
	limits := normalizeExtractionLimits(ArchiveExtractionLimits{})
	const workspaceBound = int64(3 << 30)
	for name, value := range map[string]int64{
		"total":   limits.MaxTotalBytes,
		"entry":   limits.MaxEntryBytes,
		"archive": limits.MaxArchiveBytes,
	} {
		if value != workspaceBound {
			t.Fatalf("default %s limit = %d, want %d", name, value, workspaceBound)
		}
	}
	oversized := normalizeExtractionLimits(ArchiveExtractionLimits{
		MaxTotalBytes: 1 << 40, MaxEntryBytes: 1 << 40, MaxArchiveBytes: 1 << 40,
	})
	if oversized.MaxTotalBytes > workspaceBound || oversized.MaxEntryBytes > workspaceBound || oversized.MaxArchiveBytes > workspaceBound {
		t.Fatalf("oversized limits escaped workspace bound: %+v", oversized)
	}
}

func TestExtractArchiveAtomicAndSafe(t *testing.T) {
	archivePath := writeTestArchive(t, []testArchiveEntry{{name: "config.json", body: []byte("{}")}, {name: "nested/weights.bin", body: []byte("weights")}})
	destination := filepath.Join(t.TempDir(), "models", "version-id")
	if err := ExtractArchive(context.Background(), archivePath, destination); err != nil {
		t.Fatalf("ExtractArchive() error = %v", err)
	}
	for name, want := range map[string]string{"config.json": "{}", "nested/weights.bin": "weights"} {
		got, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if err := ExtractArchive(context.Background(), archivePath, destination); err != nil {
		t.Fatalf("restart extraction should reuse committed directory: %v", err)
	}
}

func TestExtractArchiveWritesArchiveFingerprintMarker(t *testing.T) {
	archivePath := writeTestArchive(t, []testArchiveEntry{{name: "config.json", body: []byte("{}")}})
	archiveData, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(archiveData)
	destination := filepath.Join(t.TempDir(), "version-id")
	if err := ExtractArchive(context.Background(), archivePath, destination); err != nil {
		t.Fatalf("ExtractArchive() error = %v", err)
	}
	markerData, err := os.ReadFile(filepath.Join(destination, extractionMarker))
	if err != nil {
		t.Fatal(err)
	}
	var marker extractionMarkerDocument
	if err := json.Unmarshal(markerData, &marker); err != nil {
		t.Fatalf("marker JSON = %q: %v", markerData, err)
	}
	if marker.ArchiveSizeBytes != int64(len(archiveData)) || marker.ArchiveSHA256 != fmt.Sprintf("%x", expected) {
		t.Fatalf("marker = %+v, want size=%d sha256=%x", marker, len(archiveData), expected)
	}
}

func TestExtractArchiveDoesNotReuseForgedOrSymlinkMarker(t *testing.T) {
	archivePath := writeTestArchive(t, []testArchiveEntry{{name: "config.json", body: []byte("{}")}})
	for _, tc := range []struct {
		name   string
		marker func(t *testing.T, destination string)
	}{
		{
			name: "forged marker",
			marker: func(t *testing.T, destination string) {
				t.Helper()
				if err := os.MkdirAll(destination, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(destination, extractionMarker), []byte("forged\n"), 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "marker symlink",
			marker: func(t *testing.T, destination string) {
				t.Helper()
				if err := os.MkdirAll(destination, 0700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "marker-target")
				if err := os.WriteFile(target, []byte("valid-looking marker\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(destination, extractionMarker)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "version-id")
			tc.marker(t, destination)
			if err := ExtractArchive(context.Background(), archivePath, destination); err == nil {
				t.Fatal("ExtractArchive() reused an untrusted marker")
			}
		})
	}
}

func TestExtractArchiveDoesNotReuseStaleArchiveMarker(t *testing.T) {
	archivePath := writeTestArchive(t, []testArchiveEntry{{name: "config.json", body: []byte("original")}})
	destination := filepath.Join(t.TempDir(), "version-id")
	if err := ExtractArchive(context.Background(), archivePath, destination); err != nil {
		t.Fatalf("initial ExtractArchive() error = %v", err)
	}
	replacement := writeTestArchive(t, []testArchiveEntry{{name: "config.json", body: []byte("replacement")}})
	if err := os.Rename(replacement, archivePath); err != nil {
		t.Fatalf("replace archive: %v", err)
	}
	if err := ExtractArchive(context.Background(), archivePath, destination); err == nil {
		t.Fatal("ExtractArchive() reused a marker for a different archive")
	}
}

func TestExtractArchiveRejectsUnsafeAndBombEntries(t *testing.T) {
	cases := []struct {
		name  string
		entry testArchiveEntry
	}{
		{name: "absolute", entry: testArchiveEntry{name: "/etc/passwd", body: []byte("x")}},
		{name: "traversal", entry: testArchiveEntry{name: "../escape", body: []byte("x")}},
		{name: "symlink", entry: testArchiveEntry{name: "link", typeflag: tar.TypeSymlink, linkname: "outside"}},
		{name: "hardlink", entry: testArchiveEntry{name: "link", typeflag: tar.TypeLink, linkname: "outside"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archivePath := writeTestArchive(t, []testArchiveEntry{tc.entry})
			destination := filepath.Join(t.TempDir(), "version-id")
			if err := ExtractArchive(context.Background(), archivePath, destination); err == nil {
				t.Fatal("unsafe archive entry was accepted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("destination = %v, want no committed destination", err)
			}
		})
	}
	duplicate := writeTestArchive(t, []testArchiveEntry{{name: "same", body: []byte("a")}, {name: "same", body: []byte("b")}})
	if err := ExtractArchive(context.Background(), duplicate, filepath.Join(t.TempDir(), "version-id")); err == nil {
		t.Fatal("duplicate archive entries were accepted")
	}
}

type testArchiveEntry struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}

func writeTestArchive(t *testing.T, entries []testArchiveEntry) string {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.body)), Typeflag: typeflag, Linkname: entry.linkname}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "model.tar.gz")
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func descriptor(url, name, body string) Descriptor {
	sum := sha256.Sum256([]byte(body))
	return Descriptor{URL: url, Filename: name, ExpectedSize: int64(len(body)), ExpectedSHA256: fmt.Sprintf("%x", sum), AllowInsecureHTTP: true}
}

func TestDownloadRejectsUnsupportedURLScheme(t *testing.T) {
	d := descriptor("ftp://object.invalid/model.bin", "model.bin", "data")
	d.AllowInsecureHTTP = false
	if err := Download(context.Background(), d, nil, t.TempDir()); err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("err=%v, want URL scheme rejection", err)
	}
}

func TestDownloadRejectsHTTPWithoutExplicitOptIn(t *testing.T) {
	d := descriptor("http://object.invalid/model.bin", "model.bin", "data")
	d.AllowInsecureHTTP = false
	if err := Download(context.Background(), d, nil, t.TempDir()); err == nil || !strings.Contains(err.Error(), "insecure") {
		t.Fatalf("err=%v, want explicit insecure HTTP opt-in rejection", err)
	}
}

func TestDownloadUsesBoundedHTTPTimeout(t *testing.T) {
	previous := modelDownloadHTTPTimeout
	modelDownloadHTTPTimeout = 10 * time.Millisecond
	t.Cleanup(func() { modelDownloadHTTPTimeout = previous })
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	d := descriptor("https://object.invalid/model.bin", "model.bin", "data")
	started := time.Now()
	err := Download(context.Background(), d, client, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "download request failed") {
		t.Fatalf("err=%v, want bounded request failure", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDownloadWritesVerifiedFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("model-data")) }))
	defer srv.Close()
	d := descriptor(srv.URL, "model.bin", "model-data")
	dir := t.TempDir()
	if err := Download(context.Background(), d, srv.Client(), dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, d.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "model-data" {
		t.Fatalf("got %q", got)
	}
}

func TestDownloadRejectsChecksumMismatchAndCleansPartial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("bad")) }))
	defer srv.Close()
	d := descriptor(srv.URL, "model.bin", "good")
	d.ExpectedSize = 3
	dir := t.TempDir()
	err := Download(context.Background(), d, srv.Client(), dir)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err=%v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("partial files remain: %v", entries)
	}
}

func TestDownloadRejectsSizeMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("data")) }))
	defer srv.Close()
	d := descriptor(srv.URL, "model.bin", "data")
	d.ExpectedSize++
	if err := Download(context.Background(), d, srv.Client(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("err=%v", err)
	}
}

func TestDownloadRejectsRedirect(t *testing.T) {
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("data")) }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL, http.StatusFound) }))
	defer src.Close()
	d := descriptor(src.URL, "model.bin", "data")
	if err := Download(context.Background(), d, src.Client(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err=%v", err)
	}
}

func TestDownloadRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	d := descriptor(srv.URL, "model.bin", "data")
	if err := Download(context.Background(), d, srv.Client(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("err=%v", err)
	}
}

func TestDownloadReusesMatchingFile(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; _, _ = w.Write([]byte("data")) }))
	defer srv.Close()
	d := descriptor(srv.URL, "model.bin", "data")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, d.Filename), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), d, srv.Client(), dir); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("matching file was downloaded again")
	}
}

func TestDownloadDoesNotReuseMatchingSymlink(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()
	d := descriptor(srv.URL, "model.bin", "data")
	dir := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.bin")
	if err := os.WriteFile(external, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, d.Filename)); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), d, srv.Client(), dir); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("matching symlink was reused")
	}
	info, err := os.Lstat(filepath.Join(dir, d.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("installed file mode = %v, want regular non-symlink", info.Mode())
	}
}
