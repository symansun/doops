package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGatewayMySQLDataDirDefault covers the D18 default: a MySQL-backed
// Gateway has no database file to derive a directory from, so the file root is
// explicit. It must land, be absolute, and be the root both filesystem-backed
// subsystems read.
func TestGatewayMySQLDataDirDefault(t *testing.T) {
	if !filepath.IsAbs(defaultGatewayMySQLDataDir) {
		t.Fatalf("defaultGatewayMySQLDataDir = %q, want an absolute path", defaultGatewayMySQLDataDir)
	}
	dir := filepath.Join(t.TempDir(), "data")
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", dir)
	got, err := gatewayMySQLDataDir()
	if err != nil {
		t.Fatalf("gatewayMySQLDataDir: %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Fatalf("data dir = %q, want %q", got, filepath.Clean(dir))
	}
	for _, name := range []string{"skills", "artifacts"} {
		sub := filepath.Join(got, name)
		info, err := os.Stat(sub)
		if err != nil {
			t.Fatalf("the data dir did not create %s: %v", sub, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", sub)
		}
		if info.Mode().Perm() != 0700 {
			t.Errorf("%s mode = %v, want 0700", sub, info.Mode().Perm())
		}
		probe, err := os.CreateTemp(sub, ".probe-")
		if err != nil {
			t.Fatalf("%s is not writable: %v", sub, err)
		}
		probeName := probe.Name()
		_ = probe.Close()
		_ = os.Remove(probeName)
	}
	store := &GatewayStore{dbDir: got, backend: "mysql", dialect: gatewayDialectMySQL}
	if want := filepath.Join(got, "skills"); store.skillStorageRoot() != want {
		t.Errorf("skillStorageRoot = %q, want %q", store.skillStorageRoot(), want)
	}
	if want := filepath.Join(got, "artifacts"); store.monitorArtifactRoot() != want {
		t.Errorf("monitorArtifactRoot = %q, want %q", store.monitorArtifactRoot(), want)
	}
	if store.StoreDataDir() != got {
		t.Errorf("StoreDataDir = %q, want %q", store.StoreDataDir(), got)
	}
}

// TestGatewayMySQLDataDirFallsBackToDefaultConstant covers the branch that
// runs in production when DOOPS_GATEWAY_DATA_DIR is absent: the resolved root is
// the fixed default, not the process working directory. The resolution itself is
// filesystem-free, so this also runs where /var/lib is not writable; the full
// function may then only fail on the write probe, naming that same default path.
func TestGatewayMySQLDataDirFallsBackToDefaultConstant(t *testing.T) {
	got, err := gatewayMySQLDataDirPath("")
	if err != nil {
		t.Fatalf("resolve the default data dir: %v", err)
	}
	if got != defaultGatewayMySQLDataDir {
		t.Fatalf("empty DOOPS_GATEWAY_DATA_DIR resolved to %q, want the default %q", got, defaultGatewayMySQLDataDir)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("default data dir %q is not absolute", got)
	}
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", "")
	resolved, err := gatewayMySQLDataDir()
	if err == nil {
		if resolved != defaultGatewayMySQLDataDir {
			t.Fatalf("unset DOOPS_GATEWAY_DATA_DIR resolved to %q, want %q", resolved, defaultGatewayMySQLDataDir)
		}
		return
	}
	if !strings.HasPrefix(err.Error(), "gateway_data_dir_unwritable: "+defaultGatewayMySQLDataDir) {
		t.Fatalf("unset DOOPS_GATEWAY_DATA_DIR error = %q, want the default %q to be the path that failed", err, defaultGatewayMySQLDataDir)
	}
}

// TestGatewayMySQLDataDirRejectsRelativePath: a relative root would silently
// depend on the process working directory, so it is a startup error.
func TestGatewayMySQLDataDirRejectsRelativePath(t *testing.T) {
	if _, err := gatewayMySQLDataDirPath("relative/data"); err == nil || !strings.HasPrefix(err.Error(), "gateway_data_dir_invalid:") {
		t.Fatalf("gatewayMySQLDataDirPath(relative) error = %v, want gateway_data_dir_invalid prefix", err)
	}
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", "relative/data")
	_, err := gatewayMySQLDataDir()
	if err == nil {
		t.Fatal("a relative data dir was accepted")
	}
	if !strings.HasPrefix(err.Error(), "gateway_data_dir_invalid:") {
		t.Fatalf("relative data dir error = %q, want gateway_data_dir_invalid prefix", err)
	}
}

// TestGatewayMySQLDataDirRejectsUnwritable: a root that cannot take a file is a
// startup error, because otherwise Skill bundles and File-First artifacts quietly
// stay in database columns.
func TestGatewayMySQLDataDirRejectsUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write into a 0500 directory")
	}
	base := t.TempDir()
	// Pre-create both subdirectories and lock them: MkdirAll then succeeds, so
	// only the write probe can catch the unwritable root. Locking the parent
	// instead would fail in MkdirAll and never exercise the probe.
	for _, name := range []string{"skills", "artifacts"} {
		sub := filepath.Join(base, name)
		if err := os.MkdirAll(sub, 0700); err != nil {
			t.Fatalf("create %s: %v", sub, err)
		}
		if err := os.Chmod(sub, 0500); err != nil {
			t.Fatalf("chmod %s: %v", sub, err)
		}
		t.Cleanup(func() { _ = os.Chmod(sub, 0700) })
	}
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", base)
	_, err := gatewayMySQLDataDir()
	if err == nil {
		t.Fatal("an unwritable data dir was accepted")
	}
	if !strings.HasPrefix(err.Error(), "gateway_data_dir_unwritable:") {
		t.Fatalf("unwritable data dir error = %q, want gateway_data_dir_unwritable prefix", err)
	}
}

// TestGatewayStoreLocationFollowsBackend covers the operator-facing log fix: a
// MySQL store must not report the SQLite path it never opened.
func TestGatewayStoreLocationFollowsBackend(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	defer store.Close()
	if store.StoreBackend() != "sqlite" {
		t.Fatalf("StoreBackend = %q, want sqlite", store.StoreBackend())
	}
	if store.StoreLocation() != dbPath {
		t.Errorf("SQLite StoreLocation = %q, want %q", store.StoreLocation(), dbPath)
	}
	if store.StoreDataDir() != filepath.Dir(dbPath) {
		t.Errorf("SQLite StoreDataDir = %q, want %q", store.StoreDataDir(), filepath.Dir(dbPath))
	}
	mysqlStore := &GatewayStore{backend: "mysql", mysqlAddr: "127.0.0.1:13306", mysqlDatabase: "doops_e2e", dbDir: "/var/lib/doops-gateway"}
	if got, want := mysqlStore.StoreLocation(), "mysql 127.0.0.1:13306/doops_e2e"; got != want {
		t.Errorf("MySQL StoreLocation = %q, want %q", got, want)
	}
}
