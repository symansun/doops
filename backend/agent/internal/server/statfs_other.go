//go:build !linux

package server

import "syscall"

func statfsBlockSize(st syscall.Statfs_t) float64 {
	return float64(st.Bsize)
}
