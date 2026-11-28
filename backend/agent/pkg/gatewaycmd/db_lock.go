package gatewaycmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func canonicalGatewayDBPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultGatewayDB
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// An absent database is valid; a dangling database symlink is not.
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func acquireGatewayDBLock(dbPath string) (*os.File, error) {
	lock, err := os.OpenFile(dbPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open gateway database lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("gateway database is already in use: %s", dbPath)
		}
		return nil, fmt.Errorf("lock gateway database: %w", err)
	}
	// Keep the sidecar inode after close: unlinking it could split lock ownership.
	// Closing the descriptor (including on process exit) releases the flock.
	return lock, nil
}
