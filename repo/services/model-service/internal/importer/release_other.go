//go:build !linux

package importer

// posix_fadvise cache-release is Linux-only; other platforms skip the hint
// and rely on fsync alone (archiveWriter tolerates a nil releaseCache).
var (
	archiveReleaseCache func(int, int64, int64, int) error
	archiveFADV         int
)
