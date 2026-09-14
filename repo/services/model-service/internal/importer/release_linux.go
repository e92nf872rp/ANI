//go:build linux

package importer

import "golang.org/x/sys/unix"

// Cache-release is wired to posix_fadvise(POSIX_FADV_DONTNEED) so streamed
// archive windows do not pin the page cache on Linux workers.
var (
	archiveReleaseCache func(int, int64, int64, int) error = unix.Fadvise
	archiveFADV         = int(unix.FADV_DONTNEED)
)
