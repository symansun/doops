package inspect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/user/doops/agent/internal/dosql/recorder"
)

// SnapshotStore persists per-database versioned SchemaIR JSON snapshots on disk
// so Drift detection (latest vs live) and version-to-version diffs work without
// re-querying historical states.
type SnapshotStore struct {
	Dir string
}

func NewSnapshotStore(stateRoot, engine, address, database string) *SnapshotStore {
	identity, _ := json.Marshal([]string{
		strings.ToLower(strings.TrimSpace(engine)),
		recorder.CanonicalAddress(address),
		strings.TrimSpace(database),
	})
	sum := sha256.Sum256(identity)
	return &SnapshotStore{
		Dir: filepath.Join(stateRoot, "snapshots", hex.EncodeToString(sum[:])),
	}
}

func (s *SnapshotStore) Save(version int64, ir *SchemaIR) (string, error) {
	if s == nil || ir == nil {
		return "", nil
	}
	if ir.SchemaHash == "" {
		ir.SchemaHash = ir.ComputeHash()
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(ir, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.Dir, fmt.Sprintf("v%06d.json", version))
	if err := writeSnapshotAtomic(path, raw, 0600); err != nil {
		return "", err
	}
	// Atomically update latest.json pointer copy; propagate errors instead of silently ignoring.
	if err := writeSnapshotAtomic(filepath.Join(s.Dir, "latest.json"), raw, 0600); err != nil {
		return path, err
	}
	return path, nil
}

func writeSnapshotAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-snapshot-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *SnapshotStore) Load(version int64) (*SchemaIR, error) {
	path := filepath.Join(s.Dir, fmt.Sprintf("v%06d.json", version))
	return readSnapshotFile(path)
}

func (s *SnapshotStore) LoadLatest() (*SchemaIR, error) {
	return readSnapshotFile(filepath.Join(s.Dir, "latest.json"))
}

func (s *SnapshotStore) ListVersions() ([]int64, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "v") && strings.HasSuffix(name, ".json") {
			if n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(name, "v"), ".json"), 10, 64); err == nil {
				out = append(out, n)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func readSnapshotFile(path string) (*SchemaIR, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ir SchemaIR
	if err := json.Unmarshal(raw, &ir); err != nil {
		return nil, err
	}
	return &ir, nil
}
