package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newDoSQLJournalPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "versions", "postgres__orders.jsonl")
}

func TestDoSQLVersionJournalAssignsOrderedVersionsAndPairsOutcomes(t *testing.T) {
	path := newDoSQLJournalPath(t)
	one, err := beginDoSQLVersion(path, "dbop_a", "cred-a", "sha256:aa", []string{"CREATE TABLE t (id int)"})
	if err != nil || one != 1 {
		t.Fatalf("first version = %d, %v", one, err)
	}
	if err := endDoSQLVersion(path, one, "dbop_a", "cred-a", "sha256:aa",
		doSQLDirectResult{Status: "verified", Committed: true}, nil); err != nil {
		t.Fatal(err)
	}
	two, err := beginDoSQLVersion(path, "dbop_b", "cred-a", "sha256:bb", []string{"INSERT INTO t VALUES (1)"})
	if err != nil || two != 2 {
		t.Fatalf("second version = %d, %v", two, err)
	}
	if err := endDoSQLVersion(path, two, "dbop_b", "cred-a", "sha256:bb",
		doSQLDirectResult{Status: "failed"}, errors.New("dosql_statement_failed: statement 1: boom")); err != nil {
		t.Fatal(err)
	}

	summaries, err := listDoSQLVersions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 ||
		summaries[0].Version != 1 || summaries[0].Status != "verified" || !summaries[0].Committed ||
		summaries[1].Version != 2 || summaries[1].Status != "failed" || summaries[1].Committed {
		t.Fatalf("unexpected summaries: %#v", summaries)
	}
	if unfinished := doSQLVersionUnfinished(summaries); len(unfinished) != 0 {
		t.Fatalf("finished versions reported unfinished: %v", unfinished)
	}
}

func TestDoSQLVersionJournalReportsABeginWithoutAnEndAsUnfinished(t *testing.T) {
	path := newDoSQLJournalPath(t)
	one, err := beginDoSQLVersion(path, "dbop_a", "cred", "sha256:aa", []string{"DROP TABLE t"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulates a crash after the reservation but before the outcome was written.
	summaries, err := listDoSQLVersions(path)
	if err != nil {
		t.Fatal(err)
	}
	unfinished := doSQLVersionUnfinished(summaries)
	if len(unfinished) != 1 || unfinished[0] != one {
		t.Fatalf("unfinished = %v, want [%d]", unfinished, one)
	}
	// The next apply must still get a fresh number and not reuse the unfinished one.
	next, err := beginDoSQLVersion(path, "dbop_b", "cred", "sha256:bb", []string{"SELECT 1"})
	if err != nil || next != one+1 {
		t.Fatalf("version after an unfinished one = %d, %v", next, err)
	}
}

func TestDoSQLVersionJournalConcurrentAppliesGetDistinctVersions(t *testing.T) {
	path := newDoSQLJournalPath(t)
	const workers = 24
	versions := make([]int64, workers)
	var wg sync.WaitGroup
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			version, err := beginDoSQLVersion(path, "dbop_"+randomHex(4), "cred", "sha256:aa", []string{"UPDATE t SET a = 1"})
			if err != nil {
				t.Errorf("begin: %v", err)
				return
			}
			versions[index] = version
		}()
	}
	wg.Wait()
	seen := map[int64]bool{}
	for _, version := range versions {
		if version < 1 || version > workers || seen[version] {
			t.Fatalf("versions are not a distinct 1..%d range: %v", workers, versions)
		}
		seen[version] = true
	}
	// A broken chain would make this fail: every concurrent append must have linked.
	if _, err := listDoSQLVersions(path); err != nil {
		t.Fatalf("hash chain broke under concurrency: %v", err)
	}
}

func TestDoSQLVersionJournalDetectsEditedAndDeletedEntries(t *testing.T) {
	build := func(t *testing.T) (string, []string) {
		path := newDoSQLJournalPath(t)
		for _, statement := range []string{"CREATE TABLE a (id int)", "INSERT INTO a VALUES (1)"} {
			version, err := beginDoSQLVersion(path, "dbop_"+randomHex(3), "cred", "sha256:aa", []string{statement})
			if err != nil {
				t.Fatal(err)
			}
			if err := endDoSQLVersion(path, version, "dbop_x", "cred", "sha256:aa",
				doSQLDirectResult{Status: "verified", Committed: true}, nil); err != nil {
				t.Fatal(err)
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return path, strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	}
	write := func(t *testing.T, path string, lines []string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("edited statement", func(t *testing.T) {
		path, lines := build(t)
		lines[0] = strings.Replace(lines[0], "CREATE TABLE a", "DROP TABLE a", 1)
		write(t, path, lines)
		if _, err := listDoSQLVersions(path); err == nil || !strings.Contains(err.Error(), "dosql_version_journal_corrupt") {
			t.Fatalf("edit was not detected: %v", err)
		}
	})
	t.Run("deleted middle entry", func(t *testing.T) {
		path, lines := build(t)
		write(t, path, append(append([]string{}, lines[:1]...), lines[2:]...))
		if _, err := listDoSQLVersions(path); err == nil || !strings.Contains(err.Error(), "dosql_version_journal_corrupt") {
			t.Fatalf("deletion was not detected: %v", err)
		}
	})
	t.Run("reordered entries", func(t *testing.T) {
		path, lines := build(t)
		lines[1], lines[2] = lines[2], lines[1]
		write(t, path, lines)
		if _, err := listDoSQLVersions(path); err == nil {
			t.Fatal("reordering was not detected")
		}
	})
	t.Run("a corrupt journal also blocks the next apply", func(t *testing.T) {
		path, lines := build(t)
		lines[0] = strings.Replace(lines[0], "CREATE TABLE a", "DROP TABLE a", 1)
		write(t, path, lines)
		if _, err := beginDoSQLVersion(path, "dbop_new", "cred", "sha256:aa", []string{"SELECT 1"}); err == nil {
			t.Fatal("apply was allowed to start on a tampered journal")
		}
	})
	t.Run("a lost end record shows up as unfinished", func(t *testing.T) {
		path, lines := build(t)
		write(t, path, lines[:len(lines)-1])
		summaries, err := listDoSQLVersions(path)
		if err != nil {
			t.Fatal(err)
		}
		if unfinished := doSQLVersionUnfinished(summaries); len(unfinished) != 1 || unfinished[0] != 2 {
			t.Fatalf("unfinished = %v, want [2]", unfinished)
		}
	})
}

func TestDoSQLVersionJournalFailsClosedWhenItCannotBeWritten(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "versions")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := beginDoSQLVersion(filepath.Join(blocker, "x.jsonl"), "dbop_a", "cred", "sha256:aa", []string{"SELECT 1"}); err == nil ||
		!strings.Contains(err.Error(), "dosql_version_journal_unavailable") {
		t.Fatalf("unwritable journal error = %v", err)
	}
}

func TestDoSQLVersionJournalNeverStoresSecretsOrUnboundedStatements(t *testing.T) {
	path := newDoSQLJournalPath(t)
	canary := "postgres://app:synthetic-journal-canary@db.invalid/app"
	long := "INSERT INTO t VALUES ('" + strings.Repeat("x", doSQLJournalStmtLimit*2) + "')"
	version, err := beginDoSQLVersion(path, "dbop_a", "cred", "sha256:aa", []string{"SELECT '" + canary + "'", long})
	if err != nil {
		t.Fatal(err)
	}
	if err := endDoSQLVersion(path, version, "dbop_a", "cred", "sha256:aa", doSQLDirectResult{Status: "failed"},
		errors.New("dosql_connect_failed: dial "+canary)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-journal-canary") {
		t.Fatalf("journal stored a credential-shaped value: %s", raw)
	}
	if len(raw) > 4*doSQLJournalStmtLimit {
		t.Fatalf("journal stored an unbounded statement: %d bytes", len(raw))
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal permissions = %v, %v", info, err)
	}
}

func TestDoSQLVersionJournalPathIsPerEngineAndDatabase(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(t.TempDir(), "ws"))
	pg, my := doSQLVersionJournalPath("postgres", "testdb"), doSQLVersionJournalPath("mysql", "testdb")
	if pg == my {
		t.Fatal("two engines with the same database name share one version history")
	}
	if strings.Contains(filepath.Base(doSQLVersionJournalPath("postgres", "../../etc/passwd")), "/") {
		t.Fatal("database name escaped the journal directory")
	}
	if filepath.Dir(doSQLVersionJournalPath("postgres", "../../etc/passwd")) != filepath.Dir(pg) {
		t.Fatal("a hostile database name changed the journal directory")
	}
}
