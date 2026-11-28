package server

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func knowledgeBaseCall(t *testing.T, payload map[string]interface{}) string {
	t.Helper()
	payload["session_id"] = "kb-tool"
	args, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handleKnowledgeBaseCall(args)
	if err != nil {
		t.Fatalf("knowledge base %v failed: %v", payload["operation"], err)
	}
	return result
}

// The console and the agent both reach the knowledge base through one tool, so
// list, read, write and delete have to agree on the same root and reject paths
// that escape it.
func TestKnowledgeBaseToolLifecycle(t *testing.T) {
	kb := t.TempDir()
	t.Setenv("DOOPS_KB_ROOT", kb)

	knowledgeBaseCall(t, map[string]interface{}{"operation": "write", "name": "runbook.md", "content": "restart the gateway with systemctl"})

	list := knowledgeBaseCall(t, map[string]interface{}{"operation": "list"})
	var parsed struct {
		Root      string `json:"root"`
		OnVolume  bool   `json:"on_volume"`
		Documents []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"documents"`
		Usage struct {
			Documents int64 `json:"documents"`
			Max       int64 `json:"max_documents"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(list), &parsed); err != nil {
		t.Fatalf("list result is not JSON: %v (%q)", err, list)
	}
	if parsed.Root != kb {
		t.Fatalf("list root = %q, want %q", parsed.Root, kb)
	}
	if len(parsed.Documents) != 1 || parsed.Documents[0].Name != "runbook.md" || parsed.Documents[0].Size == 0 {
		t.Fatalf("list documents = %+v", parsed.Documents)
	}
	if parsed.Usage.Documents != 1 {
		t.Fatalf("list usage = %+v", parsed.Usage)
	}

	content := knowledgeBaseCall(t, map[string]interface{}{"operation": "read", "name": "runbook.md"})
	if content != "restart the gateway with systemctl" {
		t.Fatalf("read returned %q", content)
	}

	knowledgeBaseCall(t, map[string]interface{}{"operation": "delete", "name": "runbook.md"})
	if _, err := os.Stat(filepath.Join(kb, "runbook.md")); !os.IsNotExist(err) {
		t.Fatalf("delete left the document in place: %v", err)
	}
	trashEntries, err := os.ReadDir(filepath.Join(kb, knowledgeBaseTrashDir))
	if err != nil || len(trashEntries) != 1 {
		t.Fatalf("delete did not keep a recoverable copy: %v %v", trashEntries, err)
	}
	if !strings.HasSuffix(trashEntries[0].Name(), "runbook.md") {
		t.Fatalf("trash entry %q does not name the document", trashEntries[0].Name())
	}
}

func TestKnowledgeBaseToolRejectsPathsOutsideRoot(t *testing.T) {
	kb := t.TempDir()
	t.Setenv("DOOPS_KB_ROOT", kb)
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret.md", outside, "notes.txt", "sub/dir.md"} {
		args, err := json.Marshal(map[string]string{"session_id": "kb-tool", "operation": "read", "name": name})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := handleKnowledgeBaseCall(args); err == nil {
			t.Fatalf("read of %q was allowed", name)
		}
	}
	args, err := json.Marshal(map[string]string{"session_id": "kb-tool", "operation": "delete", "name": "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handleKnowledgeBaseCall(args); err == nil {
		t.Fatal("deleting a missing document was reported as success")
	}
}

func TestKnowledgeBaseTrashExpiresWithTheSameSchedule(t *testing.T) {
	kb := t.TempDir()
	t.Setenv("DOOPS_KB_ROOT", kb)
	trash := filepath.Join(kb, knowledgeBaseTrashDir)
	if err := os.MkdirAll(trash, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(trash, "1-old.md")
	fresh := filepath.Join(trash, "2-fresh.md")
	for _, path := range []string{old, fresh} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}

	pruneKnowledgeBaseTrash(24 * time.Hour)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expired trash entry survived: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh trash entry was removed: %v", err)
	}
}

// The audit line names the document, never its body: knowledge notes carry
// operational detail that must not be duplicated into the audit log.
func TestKnowledgeBaseAuditSummaryOmitsContent(t *testing.T) {
	body := "database password hint: rotate the batch credential"
	args, err := json.Marshal(map[string]interface{}{
		"session_id": "kb-tool",
		"operation":  "write",
		"name":       "secrets.md",
		"content":    body,
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatal(err)
	}
	summary := knowledgeBaseToolSummary(parsed, args)
	if strings.Contains(summary, "rotate the batch credential") {
		t.Fatalf("audit summary leaked the document body: %q", summary)
	}
	if !strings.Contains(summary, "secrets.md") || !strings.Contains(summary, "write") {
		t.Fatalf("audit summary lost the operation or document name: %q", summary)
	}
	if action := knowledgeBaseToolAction(args); action != ActionWrite {
		t.Fatalf("write action = %q", action)
	}
	readArgs, _ := json.Marshal(map[string]string{"operation": "read", "name": "secrets.md"})
	if action := knowledgeBaseToolAction(readArgs); action != ActionRead {
		t.Fatalf("read action = %q", action)
	}
	unknown, _ := json.Marshal(map[string]string{"operation": "drop"})
	if action := knowledgeBaseToolAction(unknown); action != "" {
		t.Fatalf("unknown operation action = %q, want empty so the Gateway rejects it", action)
	}
}

// A document can embed screenshots, so an attachment is stored, listed and read
// back as base64 with its MIME type; it is not treated as a text document.
func TestKnowledgeBaseImageRoundTrip(t *testing.T) {
	kb := t.TempDir()
	t.Setenv("DOOPS_KB_ROOT", kb)

	// Minimal PNG signature plus a payload; the bytes never need to decode.
	raw := []byte("\x89PNG\r\n\x1a\n-synthetic-screenshot")
	result := knowledgeBaseCall(t, map[string]interface{}{
		"operation": "write",
		"name":      "chart.png",
		"encoding":  "base64",
		"content":   base64.StdEncoding.EncodeToString(raw),
	})
	if !strings.Contains(result, "Wrote") {
		t.Fatalf("image write result = %q", result)
	}

	read := knowledgeBaseCall(t, map[string]interface{}{"operation": "read", "name": "chart.png"})
	var payload struct {
		Kind     string `json:"kind"`
		Mime     string `json:"mime"`
		Encoding string `json:"encoding"`
		Data     string `json:"data"`
	}
	if err := json.Unmarshal([]byte(read), &payload); err != nil {
		t.Fatalf("image read is not JSON: %v (%q)", err, read)
	}
	if payload.Kind != "image" || payload.Mime != "image/png" || payload.Encoding != "base64" {
		t.Fatalf("image read payload = %+v", payload)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil || string(decoded) != string(raw) {
		t.Fatalf("image bytes did not round trip: %v", err)
	}

	list := knowledgeBaseCall(t, map[string]interface{}{"operation": "list"})
	var listed struct {
		Documents []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"documents"`
	}
	if err := json.Unmarshal([]byte(list), &listed); err != nil {
		t.Fatalf("list result is not JSON: %v", err)
	}
	if len(listed.Documents) != 1 || listed.Documents[0].Kind != "image" {
		t.Fatalf("list documents = %+v", listed.Documents)
	}

	// An extension the browser would execute is not an accepted image.
	args, _ := json.Marshal(map[string]string{
		"session_id": "kb-tool", "operation": "write", "name": "chart.svg",
		"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("<svg/>")),
	})
	if _, err := handleKnowledgeBaseCall(args); err == nil {
		t.Fatal("svg attachment was accepted")
	}
}
