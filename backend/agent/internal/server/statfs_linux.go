//go:build linux

package server

import "syscall"

// statfsBlockSize returns the POSIX fragment size used to scale Statfs_t.Blocks/Bfree/Bavail.
// On Linux FUSE/virtiofs/fakeowner mounts, Bsize is the optimal I/O transfer size (often 1 MiB)
// while Blocks counts Frsize units (typically 4 KiB).
func statfsBlockSize(st syscall.Statfs_t) float64 {
	if st.Frsize > 0 {
		return float64(st.Frsize)
	}
	return float64(st.Bsize)
}
