package importer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

type fixtureSource struct {
	files []RemoteFile
	data  map[string]string
}

type recordingArchiveFile struct {
	bytes.Buffer
	syncs int
}

func (f *recordingArchiveFile) Sync() error {
	f.syncs++
	return nil
}

func (f *recordingArchiveFile) Fd() uintptr { return 42 }

func (f fixtureSource) List(context.Context, Repository) ([]RemoteFile, error) {
	return append([]RemoteFile(nil), f.files...), nil
}

func (f fixtureSource) Open(_ context.Context, _ Repository, path string) (io.ReadCloser, int64, error) {
	value, ok := f.data[path]
	if !ok {
		return nil, 0, errors.New("missing fixture")
	}
	return io.NopCloser(strings.NewReader(value)), int64(len(value)), nil
}

func TestBuildArchiveIsDeterministicAndSorted(t *testing.T) {
	source := fixtureSource{
		files: []RemoteFile{{Path: "z.txt", Size: 1}, {Path: "config.json", Size: 2}},
		data:  map[string]string{"z.txt": "z", "config.json": "{}"},
	}
	one, manifestOne, err := BuildArchive(context.Background(), source, Repository{Source: "huggingface", RepoID: "org/model", Revision: "main"}, ArchiveLimits{MaxFiles: 4, MaxTotalBytes: 10, MaxFileBytes: 5})
	if err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}
	first, _ := io.ReadAll(one)
	_ = one.Close()
	two, manifestTwo, err := BuildArchive(context.Background(), source, Repository{Source: "huggingface", RepoID: "org/model", Revision: "main"}, ArchiveLimits{MaxFiles: 4, MaxTotalBytes: 10, MaxFileBytes: 5})
	if err != nil {
		t.Fatalf("second BuildArchive() error = %v", err)
	}
	second, _ := io.ReadAll(two)
	_ = two.Close()
	if !bytes.Equal(first, second) || manifestOne != manifestTwo {
		t.Fatalf("archive is not deterministic: manifests %+v/%+v, bytes equal=%v", manifestOne, manifestTwo, bytes.Equal(first, second))
	}
	if manifestOne.FileCount != 2 || manifestOne.TotalBytes != 3 || manifestOne.SHA256 != sha256Hex(first) {
		t.Fatalf("manifest = %+v, want count=2 total=3 sha256 archive", manifestOne)
	}
	reader, err := gzip.NewReader(bytes.NewReader(first))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	tarReader := tar.NewReader(reader)
	var names []string
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() error = %v", err)
		}
		names = append(names, header.Name)
		if header.Typeflag != tar.TypeReg || header.ModTime.Unix() != 0 || header.Uid != 0 || header.Gid != 0 {
			t.Fatalf("unsafe/non-deterministic header = %+v", header)
		}
	}
	if strings.Join(names, ",") != "config.json,z.txt" {
		t.Fatalf("archive names = %v, want lexical order", names)
	}
}

func TestBuildArchiveRejectsUnsafeEntriesAndLimits(t *testing.T) {
	cases := []struct {
		name   string
		files  []RemoteFile
		limits ArchiveLimits
	}{
		{name: "traversal", files: []RemoteFile{{Path: "../secret", Size: 1}}},
		{name: "absolute", files: []RemoteFile{{Path: "/secret", Size: 1}}},
		{name: "duplicate", files: []RemoteFile{{Path: "a", Size: 1}, {Path: "a", Size: 1}}},
		{name: "symlink", files: []RemoteFile{{Path: "link", Type: "symlink", Size: 1}}},
		{name: "hardlink", files: []RemoteFile{{Path: "link", Type: "hardlink", Size: 1}}},
		{name: "file-count", files: []RemoteFile{{Path: "a", Size: 1}, {Path: "b", Size: 1}}, limits: ArchiveLimits{MaxFiles: 1}},
		{name: "total-size", files: []RemoteFile{{Path: "a", Size: 4}}, limits: ArchiveLimits{MaxTotalBytes: 3}},
		{name: "file-size", files: []RemoteFile{{Path: "a", Size: 4}}, limits: ArchiveLimits{MaxFileBytes: 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := fixtureSource{files: tc.files, data: map[string]string{"a": "abcd", "b": "b", "../secret": "x", "/secret": "x", "link": "x"}}
			if archive, _, err := BuildArchive(context.Background(), source, Repository{Source: "huggingface", RepoID: "org/model", Revision: "main"}, tc.limits); err == nil {
				_ = archive.Close()
				t.Fatal("BuildArchive() succeeded, want rejection")
			}
		})
	}
}

func TestBuildArchiveRejectsSizeMismatch(t *testing.T) {
	source := fixtureSource{files: []RemoteFile{{Path: "model.bin", Size: 4}}, data: map[string]string{"model.bin": "too-long"}}
	if archive, _, err := BuildArchive(context.Background(), source, Repository{Source: "modelscope", RepoID: "org/model", Revision: "main"}, ArchiveLimits{}); err == nil {
		_ = archive.Close()
		t.Fatal("BuildArchive() accepted size mismatch")
	}
}

func TestArchiveWriterSyncsAndReleasesCompletedCacheWindows(t *testing.T) {
	file := &recordingArchiveFile{}
	var releasedOffset, releasedLength int64
	writer := &archiveWriter{
		file:       file,
		digest:     sha256.New(),
		max:        32,
		flushEvery: 4,
		releaseCache: func(_ int, offset, length int64, _ int) error {
			releasedOffset = offset
			releasedLength = length
			return nil
		},
	}

	if count, err := writer.Write([]byte("12345")); err != nil || count != 5 {
		t.Fatalf("Write() = %d, %v; want 5, nil", count, err)
	}
	if file.syncs != 1 || releasedOffset != 0 || releasedLength != 5 {
		t.Fatalf("flush = syncs %d offset %d length %d; want 1, 0, 5", file.syncs, releasedOffset, releasedLength)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
