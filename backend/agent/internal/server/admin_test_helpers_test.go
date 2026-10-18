package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newAdminStoreAndToken(t *testing.T) (*GatewayStore, string) {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	user, err := store.CreateUser("admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster:  "*",
		Instance: "*",
		Actions:  []GatewayAction{ActionAdmin},
	}); err != nil {
		t.Fatalf("grant admin: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "admin"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return store, token.Plaintext
}

func createLocalBareGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "repo.git")
	runGit(t, root, "init", "--bare", bare)
	runGit(t, root, "init", "-b", "main", work)
	runGit(t, work, "config", "user.email", "test@example.com")
	runGit(t, work, "config", "user.name", "Doops Test")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	runGit(t, work, "add", "README.md")
	runGit(t, work, "commit", "-m", "init")
	runGit(t, work, "remote", "add", "origin", bare)
	runGit(t, work, "push", "origin", "main")
	return bare
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
