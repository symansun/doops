package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The knowledge base is read back into an agent or console session, so a
// document larger than one read is unreachable once written. Saves must refuse
// it instead of storing a file the read path will always reject.
func TestKnowledgeBaseSaveRejectsOversizedDocument(t *testing.T) {
	kb := t.TempDir()
	t.Setenv("DOOPS_KB_ROOT", kb)
	t.Setenv("DOOPS_MAX_FILE_WRITE_BYTES", "8192")

	doc := filepath.Join(kb, "运维手册.md")
	if _, err := writeKnowledgeBaseFile("kb-save", doc, "old body"); err != nil {
		t.Fatalf("seed write failed: %v", err)
	}

	_, err := writeKnowledgeBaseFile("kb-save", doc, strings.Repeat("x", int(maxKnowledgeBaseDocumentBytes)+1))
	if err == nil || !strings.Contains(err.Error(), "over the 1048576-byte limit") {
		t.Fatalf("oversized document was accepted: %v", err)
	}

	kept, readErr := os.ReadFile(doc)
	if readErr != nil {
		t.Fatalf("rejected save removed the document: %v", readErr)
	}
	if string(kept) != "old body" {
		t.Fatalf("rejected save changed the document: %q", kept)
	}
	requireDirectoryEntries(t, kb, []string{"运维手册.md"})
}

// The workspace keeps its own 16 MiB bound; only the knowledge base is tied to
// the read bound. This test fails if someone "fixes" the mismatch by lowering
// the workspace limit or by raising the read limit.
func TestWorkspaceWriteKeepsItsOwnSizeBound(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", workspace)
	t.Setenv("DOOPS_MAX_FILE_READ_BYTES", "1024")
	t.Setenv("DOOPS_MAX_FILE_WRITE_BYTES", "8192")
	if err := os.MkdirAll(filepath.Join(workspace, "kb-save"), 0o755); err != nil {
		t.Fatal(err)
	}

	payload := strings.Repeat("y", 2048)
	args, err := json.Marshal(map[string]string{
		"session_id": "kb-save",
		"path":       "notes.txt",
		"content":    payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := handleFileWrite(args)
	if err != nil {
		t.Fatalf("workspace write with the knowledge-base read bound was rejected: %v", err)
	}
	if !strings.Contains(result, fmt.Sprintf("Wrote %d bytes", len(payload))) {
		t.Fatalf("workspace write result = %q", result)
	}
	onDisk, err := os.ReadFile(filepath.Join(workspace, "kb-save", "notes.txt"))
	if err != nil {
		t.Fatalf("workspace write did not reach disk: %v", err)
	}
	if string(onDisk) != payload {
		t.Fatalf("workspace write stored %d bytes, want %d", len(onDisk), len(payload))
	}
}

// Concurrent saves of the same document must not interleave. The temporary
// file plus rename keeps every reader on a complete document and leaves no
// scratch file behind.
func TestKnowledgeBaseSaveIsAtomicUnderConcurrency(t *testing.T) {
	kb := t.TempDir()
	t.Setenv("DOOPS_KB_ROOT", kb)
	t.Setenv("DOOPS_MAX_FILE_READ_BYTES", "65536")

	doc := filepath.Join(kb, "并发.md")
	const writers = 8
	payloads := make([]string, writers)
	for i := range payloads {
		payloads[i] = strings.Repeat(string(rune('a'+i)), 4096)
	}

	var wg sync.WaitGroup
	failures := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			if _, err := writeKnowledgeBaseFile("kb-save", doc, payload); err != nil {
				failures <- err
			}
		}(payloads[i])
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("concurrent save failed: %v", err)
	}

	final, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("concurrent saves left no document: %v", err)
	}
	if !containsString(payloads, string(final)) {
		t.Fatalf("document holds a torn write of %d bytes", len(final))
	}
	requireDirectoryEntries(t, kb, []string{"并发.md"})
}

func requireDirectoryEntries(t *testing.T, dir string, want []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if strings.Join(got, ",") != strings.Join(sorted, ",") {
		t.Fatalf("%s holds %v, want %v (a scratch file was left behind?)", dir, got, sorted)
	}
}
