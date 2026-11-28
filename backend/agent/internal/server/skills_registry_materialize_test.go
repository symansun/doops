package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMaterializedBuiltinScriptsAreExecutable pins the mode of a materialized
// entry script. Both backends write Skill packages through the same function, so
// this covers SQLite and MySQL: the shipped inspect.sh is 0755 (SF-07), and the
// copy the Agent runs must not silently drop that.
func TestMaterializedBuiltinScriptsAreExecutable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	for _, slug := range []string{"inspect-vm-host", "inspect-container-runtime"} {
		script := filepath.Join(store.skillStorageRoot(), slug, "scripts", "inspect.sh")
		info, err := os.Stat(script)
		if err != nil {
			t.Fatalf("%s was not materialized: %v", script, err)
		}
		if info.Mode().Perm()&0111 == 0 {
			t.Errorf("%s mode = %v, want an executable bit", script, info.Mode().Perm())
		}
		skillMD := filepath.Join(store.skillStorageRoot(), slug, "SKILL.md")
		if info, err := os.Stat(skillMD); err != nil {
			t.Fatalf("%s was not materialized: %v", skillMD, err)
		} else if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %v, want 0600", skillMD, info.Mode().Perm())
		}
	}
}
