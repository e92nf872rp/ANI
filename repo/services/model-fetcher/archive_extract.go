package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	defaultExtractionMaxFiles = 10000
	// Keep extraction bounded by the same 3Gi source/archive policy used by
	// model-import-worker. The inference pod shares its model volume with the
	// engine, so accepting the former 1TiB library default could turn a small
	// compressed archive into an unbounded workspace exhaustion attack.
	defaultExtractionMaxTotalBytes = int64(3 << 30)
	defaultExtractionMaxEntryBytes = int64(3 << 30)
	defaultExtractionMaxArchive    = int64(3 << 30)
	extractionMarker               = ".ani-model-extraction-complete"
	maxExtractionMarkerBytes       = int64(4 << 10)
)

type archiveFingerprint struct {
	SizeBytes int64
	SHA256    string
}

type extractionMarkerDocument struct {
	ArchiveSizeBytes int64  `json:"archive_size_bytes"`
	ArchiveSHA256    string `json:"archive_sha256"`
}

// ArchiveExtractionLimits bounds both compressed input and expanded output.
// Zero values select the production defaults; callers cannot raise a bound
// above the hard package limits.
type ArchiveExtractionLimits struct {
	MaxFiles        int
	MaxTotalBytes   int64
	MaxEntryBytes   int64
	MaxArchiveBytes int64
}

// isModelArchiveObject detects the internal model-import archive convention.
// It intentionally requires an object:// URI and no query/fragment so an
// arbitrary URL cannot opt a normal single-file model into extraction.
func isModelArchiveObject(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "object" || u.Host != "models" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 5 || parts[3] != "archive" || parts[4] != "model.tar.gz" || !strings.HasPrefix(parts[2], "import-") {
		return false
	}
	for _, part := range parts[:3] {
		if part == "" || part == "." || part == ".." || strings.Contains(part, "\\") || strings.Contains(part, "..") {
			return false
		}
	}
	return true
}

// ExtractArchive validates and extracts one verified model.tar.gz archive.
// Files are written under a fresh sibling directory and committed by one
// atomic rename. A marker makes a restart after a successful extraction a
// no-op while an interrupted temporary directory is safely discarded.
func ExtractArchive(ctx context.Context, archivePath, destination string) error {
	return ExtractArchiveWithLimits(ctx, archivePath, destination, ArchiveExtractionLimits{})
}

func ExtractArchiveWithLimits(ctx context.Context, archivePath, destination string, limits ArchiveExtractionLimits) error {
	if strings.TrimSpace(archivePath) == "" || strings.TrimSpace(destination) == "" {
		return errors.New("invalid archive extraction paths")
	}
	if !filepath.IsAbs(archivePath) || !filepath.IsAbs(destination) || strings.Contains(archivePath, "\x00") || strings.Contains(destination, "\x00") {
		return errors.New("invalid archive extraction paths")
	}
	limits = normalizeExtractionLimits(limits)
	if err := ctx.Err(); err != nil {
		return err
	}
	archiveInfo, err := os.Lstat(archivePath)
	if err != nil || !archiveInfo.Mode().IsRegular() {
		return errors.New("archive file is unavailable")
	}
	if archiveInfo.Size() <= 0 || archiveInfo.Size() > limits.MaxArchiveBytes {
		return errors.New("archive size exceeds limit")
	}
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return errors.New("open archive")
	}
	defer archiveFile.Close()
	openedInfo, err := archiveFile.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() != archiveInfo.Size() {
		return errors.New("archive file changed during inspection")
	}
	fingerprint, err := fingerprintArchive(ctx, archiveFile, openedInfo.Size())
	if err != nil {
		return err
	}
	if ready, err := extractionReady(destination, fingerprint); err != nil {
		return err
	} else if ready {
		return nil
	}
	if info, err := os.Lstat(destination); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("extraction destination is a symlink")
		}
		return errors.New("extraction destination already exists")
	} else if !os.IsNotExist(err) {
		return errors.New("inspect extraction destination")
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0750); err != nil {
		return errors.New("create extraction parent")
	}
	temporary, err := os.MkdirTemp(parent, ".model-extract-*")
	if err != nil {
		return errors.New("create extraction workspace")
	}
	defer os.RemoveAll(temporary)

	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return errors.New("rewind archive")
	}
	gzipReader, err := gzip.NewReader(io.LimitReader(archiveFile, limits.MaxArchiveBytes+1))
	if err != nil {
		return errors.New("invalid model archive")
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	seen := make(map[string]struct{})
	var totalBytes int64
	var fileCount int
	for {
		if err := ctx.Err(); err != nil {
			return errors.New("archive extraction canceled")
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("read model archive")
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return err
		}
		if _, ok := seen[name]; ok {
			return errors.New("duplicate archive entry")
		}
		seen[name] = struct{}{}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return errors.New("archive contains a non-regular entry")
		}
		if header.Size < 0 || header.Size > limits.MaxEntryBytes || header.Size > limits.MaxTotalBytes-totalBytes {
			return errors.New("archive entry exceeds limit")
		}
		fileCount++
		if fileCount > limits.MaxFiles {
			return errors.New("archive file count exceeds limit")
		}
		target := filepath.Join(temporary, filepath.FromSlash(name))
		rel, err := filepath.Rel(temporary, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return errors.New("archive entry escapes destination")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return errors.New("create archive entry directory")
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("create archive entry")
		}
		written, copyErr := copyArchiveEntry(ctx, file, tarReader, header.Size)
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || written != header.Size {
			return errors.New("write archive entry")
		}
		totalBytes += written
	}
	if fileCount == 0 {
		return errors.New("archive contains no model files")
	}
	if err := gzipReader.Close(); err != nil {
		return errors.New("close model archive")
	}
	markerPath := filepath.Join(temporary, extractionMarker)
	marker, err := json.Marshal(extractionMarkerDocument{ArchiveSizeBytes: fingerprint.SizeBytes, ArchiveSHA256: fingerprint.SHA256})
	if err != nil {
		return errors.New("encode extraction marker")
	}
	marker = append(marker, '\n')
	if err := os.WriteFile(markerPath, marker, 0600); err != nil {
		return errors.New("write extraction marker")
	}
	if err := syncDirectory(temporary); err != nil {
		return errors.New("sync extraction workspace")
	}
	if err := os.Rename(temporary, destination); err != nil {
		return errors.New("commit extracted model")
	}
	if err := syncDirectory(parent); err != nil {
		return errors.New("sync extraction parent")
	}
	return nil
}

func normalizeExtractionLimits(limits ArchiveExtractionLimits) ArchiveExtractionLimits {
	if limits.MaxFiles <= 0 || limits.MaxFiles > defaultExtractionMaxFiles {
		limits.MaxFiles = defaultExtractionMaxFiles
	}
	if limits.MaxTotalBytes <= 0 || limits.MaxTotalBytes > defaultExtractionMaxTotalBytes {
		limits.MaxTotalBytes = defaultExtractionMaxTotalBytes
	}
	if limits.MaxEntryBytes <= 0 || limits.MaxEntryBytes > defaultExtractionMaxEntryBytes {
		limits.MaxEntryBytes = defaultExtractionMaxEntryBytes
	}
	if limits.MaxArchiveBytes <= 0 || limits.MaxArchiveBytes > defaultExtractionMaxArchive {
		limits.MaxArchiveBytes = defaultExtractionMaxArchive
	}
	return limits
}

func safeArchiveName(raw string) (string, error) {
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, "\\") || strings.ContainsRune(raw, '\x00') {
		return "", errors.New("archive contains an unsafe path")
	}
	for _, r := range raw {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return "", errors.New("archive contains an unsafe path")
		}
	}
	clean := path.Clean(raw)
	if clean == "." || clean != raw || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", errors.New("archive contains an unsafe path")
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("archive contains an unsafe path")
		}
	}
	return clean, nil
}

func copyArchiveEntry(ctx context.Context, destination io.Writer, source io.Reader, size int64) (int64, error) {
	if size < 0 {
		return 0, errors.New("invalid archive entry size")
	}
	limited := io.LimitReader(source, size+1)
	var written int64
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		count, err := limited.Read(buffer)
		if count > 0 {
			n, writeErr := destination.Write(buffer[:count])
			written += int64(n)
			if writeErr != nil || n != count {
				return written, errors.New("write archive entry")
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return written, nil
			}
			return written, err
		}
	}
}

func fingerprintArchive(ctx context.Context, archiveFile *os.File, size int64) (archiveFingerprint, error) {
	if archiveFile == nil || size <= 0 {
		return archiveFingerprint{}, errors.New("invalid archive fingerprint input")
	}
	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return archiveFingerprint{}, errors.New("rewind archive")
	}
	hash := sha256.New()
	buffer := make([]byte, 32*1024)
	var read int64
	for read < size {
		if err := ctx.Err(); err != nil {
			return archiveFingerprint{}, errors.New("archive fingerprint canceled")
		}
		want := int64(len(buffer))
		if remaining := size - read; remaining < want {
			want = remaining
		}
		count, err := archiveFile.Read(buffer[:want])
		if count > 0 {
			if _, writeErr := hash.Write(buffer[:count]); writeErr != nil {
				return archiveFingerprint{}, errors.New("hash archive")
			}
			read += int64(count)
		}
		if err != nil {
			if errors.Is(err, io.EOF) && read == size {
				break
			}
			return archiveFingerprint{}, errors.New("read archive for fingerprint")
		}
	}
	return archiveFingerprint{SizeBytes: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func extractionReady(destination string, fingerprint archiveFingerprint) (bool, error) {
	info, err := os.Lstat(destination)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("extraction destination is a symlink")
	}
	if !info.IsDir() {
		return false, errors.New("extraction destination is not a directory")
	}
	marker, err := os.Lstat(filepath.Join(destination, extractionMarker))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, errors.New("inspect extraction marker")
	}
	if marker.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("extraction marker is a symlink")
	}
	if !marker.Mode().IsRegular() || marker.Size() <= 0 || marker.Size() > maxExtractionMarkerBytes {
		return false, errors.New("invalid extraction marker")
	}
	contents, err := os.ReadFile(filepath.Join(destination, extractionMarker))
	if err != nil {
		return false, errors.New("read extraction marker")
	}
	var document extractionMarkerDocument
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || !validArchiveDigest(document.ArchiveSHA256) || document.ArchiveSizeBytes <= 0 {
		return false, errors.New("invalid extraction marker")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return false, errors.New("invalid extraction marker")
	}
	if document.ArchiveSizeBytes != fingerprint.SizeBytes || !strings.EqualFold(document.ArchiveSHA256, fingerprint.SHA256) {
		return false, errors.New("extraction marker does not match archive")
	}
	return true, nil
}

func validArchiveDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func syncDirectory(name string) error {
	directory, err := os.Open(name)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}
