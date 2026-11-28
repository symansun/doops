package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// detachedOutputRun drives one detached doops_bg call through the real WS/relay
// path, streams the given chunks as progress notifications, then completes it.
func detachedOutputRun(t *testing.T, chunks []string) (int64, *executionLifecycleFixture, string, string) {
	t.Helper()
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "detached-output-agent")
	conn := dialGatewayRPCClient(t, f.server.URL, f.admin.Plaintext, "dev", "local")
	detachedSend(t, conn, 71, map[string]interface{}{"command": "probe", "detach": true})
	notice := readUntilMethod(t, conn, "notifications/doops/background_task")
	auditID := detachedNotificationAuditID(t, notice)
	call := agent.call(t, "doops_bg", "detached-session")
	userID := detachedUserID(t, f, f.admin)
	for _, chunk := range chunks {
		detachedProgress(t, agent, call, chunk)
	}
	// Wait until the accumulated output stops growing.
	last := -1
	for i := 0; i < 150; i++ {
		cur := 0
		if task := f.hub.client.detached.get(auditID, userID); task != nil {
			cur = len(task.output)
		}
		if cur == last && cur > 0 {
			break
		}
		last = cur
		time.Sleep(30 * time.Millisecond)
	}
	agent.complete(t, call, "")
	requireEventually(t, 3*time.Second, func() bool {
		task := f.hub.client.detached.get(auditID, userID)
		return task != nil && task.Status == "success"
	})
	loaded, err := f.store.loadDetachedTasks()
	if err != nil {
		t.Fatal(err)
	}
	db := ""
	for _, task := range loaded {
		if task.AuditID == auditID {
			db = task.output
		}
	}
	return auditID, f, f.hub.client.detached.get(auditID, userID).view(true).Output, db
}

// Given one 2938-byte progress chunk, the whole chunk is kept: the registry used
// to run every chunk through the 1200-byte error-text tail.
func TestDetachedOutputKeepsHeadOfLongChunk(t *testing.T) {
	chunk := "START-OF-COMMAND-OUTPUT|" + strings.Repeat("x", 2900) + "|END-OF-OUTPUT"
	auditID, f, inMemory, db := detachedOutputRun(t, []string{chunk})
	if len(inMemory) != len(chunk) {
		t.Fatalf("registry output = %d bytes, want the whole %d-byte chunk", len(inMemory), len(chunk))
	}
	if !strings.HasPrefix(inMemory, "START-OF-COMMAND-OUTPUT") || !strings.HasSuffix(inMemory, "END-OF-OUTPUT") {
		t.Fatalf("chunk head or tail was lost: %q", inMemory[:16])
	}
	if len(db) != len(chunk) {
		t.Fatalf("stored output = %d bytes, want %d", len(db), len(chunk))
	}
	view := detachedDetail(t, f, f.admin, auditID, http.StatusOK)
	out, _ := view["output"].(string)
	if len(out) != len(chunk) || !strings.HasPrefix(out, "START-OF-COMMAND-OUTPUT") {
		t.Fatalf("detail view returned %d bytes: %q", len(out), out[:16])
	}
}

// Given credentials split across two chunks, the joined text must be redacted at
// both boundaries; neither half alone matches a redaction pattern.
func TestDetachedOutputRedactsSplitCredential(t *testing.T) {
	const canary = "canarySplitValue9d2e61"
	auditID, f, inMemory, db := detachedOutputRun(t, []string{"https://alice:", canary + "@git.example.test/group/repo.git"})
	if strings.Contains(inMemory, canary) || strings.Contains(db, canary) {
		t.Fatalf("split credential survived: memory=%q db=%q", inMemory, db)
	}
	if !strings.Contains(inMemory, "https://...REDACTED") || !strings.Contains(inMemory, "git.example.test") {
		t.Fatalf("split credential was not redacted in place: %q", inMemory)
	}
	view := detachedDetail(t, f, f.admin, auditID, http.StatusOK)
	if out, _ := view["output"].(string); strings.Contains(out, canary) {
		t.Fatalf("detail view leaked the split credential: %q", out)
	}
}

// Given output past detachedOutputLimit, the tail trim lands on a rune boundary.
func TestDetachedOutputTrimIsRuneSafe(t *testing.T) {
	chunks := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		chunks = append(chunks, strings.Repeat("中", 400))
	}
	_, _, inMemory, db := detachedOutputRun(t, chunks)
	if len(inMemory) > detachedOutputLimit {
		t.Fatalf("output grew past the limit: %d", len(inMemory))
	}
	if !utf8.ValidString(inMemory) {
		t.Fatalf("registry output is not valid UTF-8: %d bytes", len(inMemory))
	}
	if !utf8.ValidString(db) {
		t.Fatalf("stored output is not valid UTF-8: %d bytes", len(db))
	}
	if strings.ContainsRune(inMemory, utf8.RuneError) {
		t.Fatalf("registry output contains U+FFFD")
	}
}

// Given a task row written at terminal state, the store keeps the whole output
// instead of re-truncating it to the 1200-byte error tail.
func TestDetachedOutputStoreUpsertDoesNotTruncate(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	body := "UPSERT-HEAD|" + strings.Repeat("z", 3000)
	if err := f.store.upsertDetachedTask(detachedTask{
		AuditID: 99001, OperationID: "op-upsert", UserID: "probe", Cluster: "dev", Instance: "local",
		Status: "success", StartedAt: time.Now().UTC(), output: body,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.store.loadDetachedTasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range loaded {
		if got.AuditID != 99001 {
			continue
		}
		if len(got.output) != len(body) || !strings.HasPrefix(got.output, "UPSERT-HEAD") {
			t.Fatalf("stored %d of %d bytes, head=%q", len(got.output), len(body), got.output[:min(16, len(got.output))])
		}
		return
	}
	t.Fatal("upserted row not found")
}

// Given a URL with userinfo and query credentials in the output, the read path
// redacts them while keeping host and path.
func TestDetachedOutputViewRedactsURLCredentials(t *testing.T) {
	const user = "detached-user-8f1"
	const canaryPass = "canaryPassValue7f3a91"
	const canaryToken = "canaryTokenValue4b8c20"
	url := fmt.Sprintf("fatal: unable to access https://%s:%s@git.example.test/group/repo.git/info/refs?token=%s&page=2: 403", user, canaryPass, canaryToken)
	auditID, f, inMemory, db := detachedOutputRun(t, []string{url})
	for name, text := range map[string]string{"memory": inMemory, "db": db} {
		if strings.Contains(text, canaryPass) || strings.Contains(text, canaryToken) {
			t.Fatalf("%s leaked a credential value: %q", name, text)
		}
	}
	if !strings.Contains(inMemory, "git.example.test/group/repo.git/info/refs") {
		t.Fatalf("host or path was dropped: %q", inMemory)
	}
	view := detachedDetail(t, f, f.admin, auditID, http.StatusOK)
	out, _ := view["output"].(string)
	if strings.Contains(out, canaryPass) || strings.Contains(out, canaryToken) {
		t.Fatalf("detail view leaked a credential value: %q", out)
	}
}
