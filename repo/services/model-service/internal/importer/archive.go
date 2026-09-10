package importer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultArchiveMaxFiles      = 10000
	defaultArchiveMaxTotalBytes = int64(1 << 40)
	defaultArchiveMaxFileBytes  = int64(1 << 40)
	defaultArchiveMaxOutput     = int64(1 << 40)
	// WorkerArchiveMaxBytes is deliberately below the model-import-worker
	// 4Gi emptyDir limit. BuildArchive uses a sibling temporary file while
	// streaming, so leaving workspace headroom avoids filling the volume with
	// archive bytes plus filesystem/page-cache overhead.
	WorkerArchiveMaxBytes = int64(3 << 30)
	archiveCacheWindow    = int64(64 << 20)
)

// ArchiveLimits bounds the untrusted source tree before it is materialized.
// Zero values use conservative defaults; limits can never be raised above the
// package-wide source bound.
type ArchiveLimits struct {
	MaxFiles       int
	MaxTotalBytes  int64
	MaxFileBytes   int64
	MaxOutputBytes int64
}

// DefaultWorkerArchiveLimits is the safe policy used by the production
// model-import worker when no optional override is supplied. It is kept
// separate from BuildArchive's broad library defaults so a worker cannot
// accidentally pair a 1TiB archive bound with its 4Gi workspace.
func DefaultWorkerArchiveLimits() ArchiveLimits {
	return ArchiveLimits{
		MaxFiles:       defaultArchiveMaxFiles,
		MaxTotalBytes:  WorkerArchiveMaxBytes,
		MaxFileBytes:   WorkerArchiveMaxBytes,
		MaxOutputBytes: WorkerArchiveMaxBytes,
	}
}

// ValidateWorkerArchiveLimits rejects policy values that cannot fit the
// worker's bounded archive workspace. Configuration loaders should call this
// before connecting to external dependencies; callers must not silently clamp
// an explicitly supplied unsafe value.
func ValidateWorkerArchiveLimits(limits ArchiveLimits) error {
	if limits.MaxFiles <= 0 || limits.MaxFiles > defaultArchiveMaxFiles {
		return fmt.Errorf("max files must be between 1 and %d", defaultArchiveMaxFiles)
	}
	for name, value := range map[string]int64{
		"max total bytes":  limits.MaxTotalBytes,
		"max file bytes":   limits.MaxFileBytes,
		"max output bytes": limits.MaxOutputBytes,
	} {
		if value <= 0 || value > WorkerArchiveMaxBytes {
			return fmt.Errorf("%s must be between 1 and %d", name, WorkerArchiveMaxBytes)
		}
	}
	if limits.MaxFileBytes > limits.MaxTotalBytes {
		return errors.New("max file bytes cannot exceed max total bytes")
	}
	return nil
}

// NormalizeWorkerArchiveLimits keeps direct Worker callers on the same safe
// defaults as the production env loader. Invalid/oversized zero-value-style
// fields are replaced with the safe default; production configuration must
// still use ValidateWorkerArchiveLimits to fail closed on explicit values.
func NormalizeWorkerArchiveLimits(limits ArchiveLimits) ArchiveLimits {
	defaults := DefaultWorkerArchiveLimits()
	if limits.MaxFiles <= 0 || limits.MaxFiles > defaults.MaxFiles {
		limits.MaxFiles = defaults.MaxFiles
	}
	if limits.MaxTotalBytes <= 0 || limits.MaxTotalBytes > WorkerArchiveMaxBytes {
		limits.MaxTotalBytes = defaults.MaxTotalBytes
	}
	if limits.MaxFileBytes <= 0 || limits.MaxFileBytes > WorkerArchiveMaxBytes {
		limits.MaxFileBytes = defaults.MaxFileBytes
	}
	if limits.MaxOutputBytes <= 0 || limits.MaxOutputBytes > WorkerArchiveMaxBytes {
		limits.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if limits.MaxFileBytes > limits.MaxTotalBytes {
		limits.MaxFileBytes = limits.MaxTotalBytes
	}
	return limits
}

// ArchiveManifest describes both source content and the resulting archive.
// SHA256 is the digest of the complete gzip stream, not of an individual file.
type ArchiveManifest struct {
	FileCount  int
	TotalBytes int64
	SizeBytes  int64
	SHA256     string
}

// ErrArchiveRejected marks deterministic validation/policy failures. Worker
// callers should persist these as terminal failures rather than redeliver a
// poison message forever. Network, source, and object-store failures do not
// wrap this sentinel and remain retryable.
var ErrArchiveRejected = errors.New("archive rejected")

func archiveRejected(message string) error { return fmt.Errorf("%w: %s", ErrArchiveRejected, message) }

var archiveEpoch = time.Unix(0, 0).UTC()

// BuildArchive builds a deterministic tar.gz stream from a public source.
// The stream is spooled to a mode-0600 temporary file so large models are not
// accumulated in process memory. The returned reader removes that file when
// closed.
func BuildArchive(ctx context.Context, source Source, repository Repository, limits ArchiveLimits) (io.ReadCloser, ArchiveManifest, error) {
	if source == nil || ValidateRepository(repository) != nil {
		return nil, ArchiveManifest{}, archiveRejected("invalid import source")
	}
	if err := ctx.Err(); err != nil {
		return nil, ArchiveManifest{}, errors.New("archive build canceled")
	}
	limits = normalizeArchiveLimits(limits)
	files, err := source.List(ctx, repository)
	if err != nil {
		return nil, ArchiveManifest{}, errors.New("list source files")
	}
	files, err = sortAndValidateFiles(files)
	if err != nil {
		return nil, ArchiveManifest{}, archiveRejected("source file list is invalid")
	}
	if len(files) == 0 {
		return nil, ArchiveManifest{}, archiveRejected("source contains no regular files")
	}
	if len(files) > limits.MaxFiles {
		return nil, ArchiveManifest{}, archiveRejected("source file count exceeds limit")
	}

	temporary, err := os.CreateTemp("", "ani-model-import-*.tar.gz")
	if err != nil {
		return nil, ArchiveManifest{}, errors.New("create archive workspace")
	}
	temporaryName := temporary.Name()
	removeTemporary := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}

	digest := sha256.New()
	writer := &archiveWriter{
		file:         temporary,
		digest:       digest,
		max:          limits.MaxOutputBytes,
		flushEvery:   archiveCacheWindow,
		releaseCache: unix.Fadvise,
	}
	gzipWriter := gzip.NewWriter(writer)
	gzipWriter.Header = gzip.Header{ModTime: archiveEpoch, OS: 255}
	tarWriter := tar.NewWriter(gzipWriter)
	manifest := ArchiveManifest{FileCount: len(files)}

	for _, file := range files {
		if err := ctx.Err(); err != nil {
			removeTemporary()
			return nil, ArchiveManifest{}, errors.New("archive build canceled")
		}
		if file.Size > limits.MaxFileBytes || file.Size > limits.MaxTotalBytes-manifest.TotalBytes {
			removeTemporary()
			return nil, ArchiveManifest{}, archiveRejected("source size exceeds limit")
		}

		body, actualSize, err := source.Open(ctx, repository, file.Path)
		if err != nil || body == nil {
			if body != nil {
				_ = body.Close()
			}
			removeTemporary()
			return nil, ArchiveManifest{}, errors.New("open source file")
		}
		if actualSize >= 0 && actualSize != file.Size {
			_ = body.Close()
			removeTemporary()
			return nil, ArchiveManifest{}, errors.New("source file size changed")
		}

		header := &tar.Header{
			Name:       file.Path,
			Mode:       0o644,
			Size:       file.Size,
			ModTime:    archiveEpoch,
			AccessTime: archiveEpoch,
			ChangeTime: archiveEpoch,
			Typeflag:   tar.TypeReg,
			Format:     tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			_ = body.Close()
			removeTemporary()
			return nil, ArchiveManifest{}, errors.New("write archive header")
		}
		written, copyErr := copyArchiveFile(ctx, tarWriter, body, file.Size+1)
		closeErr := body.Close()
		if copyErr != nil || closeErr != nil || written != file.Size {
			removeTemporary()
			return nil, ArchiveManifest{}, errors.New("read source file")
		}
		manifest.TotalBytes += written
	}
	if err := tarWriter.Close(); err != nil {
		removeTemporary()
		return nil, ArchiveManifest{}, errors.New("close archive")
	}
	if err := gzipWriter.Close(); err != nil {
		removeTemporary()
		return nil, ArchiveManifest{}, errors.New("close archive")
	}
	if err := temporary.Sync(); err != nil {
		removeTemporary()
		return nil, ArchiveManifest{}, errors.New("sync archive")
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		removeTemporary()
		return nil, ArchiveManifest{}, errors.New("rewind archive")
	}
	manifest.SizeBytes = writer.written
	manifest.SHA256 = hex.EncodeToString(digest.Sum(nil))
	return &temporaryArchive{File: temporary, path: temporaryName}, manifest, nil
}

func normalizeArchiveLimits(limits ArchiveLimits) ArchiveLimits {
	if limits.MaxFiles <= 0 || limits.MaxFiles > defaultArchiveMaxFiles {
		limits.MaxFiles = defaultArchiveMaxFiles
	}
	if limits.MaxTotalBytes <= 0 || limits.MaxTotalBytes > defaultArchiveMaxTotalBytes {
		limits.MaxTotalBytes = defaultArchiveMaxTotalBytes
	}
	if limits.MaxFileBytes <= 0 || limits.MaxFileBytes > defaultArchiveMaxFileBytes {
		limits.MaxFileBytes = defaultArchiveMaxFileBytes
	}
	if limits.MaxOutputBytes <= 0 || limits.MaxOutputBytes > defaultArchiveMaxOutput {
		limits.MaxOutputBytes = defaultArchiveMaxOutput
	}
	return limits
}

func copyArchiveFile(ctx context.Context, destination io.Writer, source io.Reader, maxBytes int64) (int64, error) {
	if maxBytes < 0 {
		return 0, errors.New("invalid source file bound")
	}
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, errors.New("archive build canceled")
		}
		remaining := maxBytes - written
		if remaining <= 0 {
			return written, nil
		}
		if int64(len(buffer)) > remaining {
			buffer = buffer[:remaining]
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			if _, err := destination.Write(buffer[:count]); err != nil {
				return written, errors.New("write archive file")
			}
			written += int64(count)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, errors.New("read source file")
		}
		if count == 0 {
			return written, io.ErrNoProgress
		}
	}
}

type archiveWriter struct {
	file         archiveFile
	digest       hash.Hash
	max          int64
	written      int64
	flushed      int64
	flushEvery   int64
	releaseCache func(int, int64, int64, int) error
}

type archiveFile interface {
	Write([]byte) (int, error)
	Sync() error
	Fd() uintptr
}

func (w *archiveWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.max-w.written {
		return 0, errors.New("archive output exceeds limit")
	}
	count, err := w.file.Write(data)
	if count > 0 {
		_, _ = w.digest.Write(data[:count])
		w.written += int64(count)
	}
	if err == nil && w.flushEvery > 0 && w.written-w.flushed >= w.flushEvery {
		if syncErr := w.file.Sync(); syncErr != nil {
			return count, errors.New("sync archive window")
		}
		if w.releaseCache != nil {
			if releaseErr := w.releaseCache(int(w.file.Fd()), w.flushed, w.written-w.flushed, unix.FADV_DONTNEED); releaseErr != nil {
				return count, errors.New("release archive cache")
			}
		}
		w.flushed = w.written
	}
	return count, err
}

type temporaryArchive struct {
	*os.File
	path string
	once sync.Once
	terr error
}

func (a *temporaryArchive) Close() error {
	a.once.Do(func() {
		closeErr := a.File.Close()
		removeErr := os.Remove(a.path)
		if closeErr != nil {
			a.terr = closeErr
		} else if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			a.terr = removeErr
		}
	})
	return a.terr
}
