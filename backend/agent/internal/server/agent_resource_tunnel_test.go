package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func connectManifestTestAgent(t *testing.T, f *executionLifecycleFixture, storage string) func() {
	t.Helper()
	conn := dialEnrolledTestAgent(t, f, "ops", "one")
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewGateway().serveWebSocketConn(conn, "manifest-test", func() {}, storage)
	}()
	t.Cleanup(func() { _ = conn.Close(); <-done })
	waitForGatewayAgent(t, f.hub, "ops", "one")
	return func() {
		_ = conn.Close()
		requireEventually(t, 3*time.Second, func() bool { return f.hub.getAgent("ops", "one") == nil })
	}
}

// Use both production ends of the reverse tunnel. A regular file at the
// manifest directory forces an actual disk error, including when run as root.
func TestTargetManifestTunnelRetryReconnectAndOfflineDirectory(t *testing.T) {
	stubKubernetesProbe(t, false)
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(dir, []byte("block manifest write"), 0600); err != nil {
		t.Fatal(err)
	}
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true, AgentLease: time.Hour})
	user := f.user(t, "manifest-user", ScopeGrant{Cluster: "ops", Instance: "one"})
	other := f.user(t, "manifest-other", ScopeGrant{Cluster: "ops", Instance: "two"})
	const path = "/v1/assets/target-manifest?cluster=ops&instance=one"
	status := func() targetManifestStatus {
		t.Helper()
		var got targetManifestStatus
		if err := json.Unmarshal(f.http(t, user, "GET", path, "", http.StatusOK), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	disconnect := connectManifestTestAgent(t, f, dir)
	f.http(t, other, "GET", path, "", http.StatusForbidden)
	f.http(t, other, "POST", "/v1/assets/target-manifest", `{"action":"refresh","cluster":"ops","instance":"one"}`, http.StatusForbidden)
	f.http(t, user, "POST", "/v1/assets/target-manifest", `{"action":"refresh","cluster":"ops","instance":"one"}`, http.StatusServiceUnavailable)
	first := status()
	agent := f.hub.getAgent("ops", "one")
	if rev := agent.snapshot().ManifestRevision; rev != 0 || first.AppliedRevision != 0 || !first.Supported || first.Manifest.Revision == 0 {
		t.Fatalf("failed disk write: execution revision %d, applied %d, desired %d, supported %t", rev, first.AppliedRevision, first.Manifest.Revision, first.Supported)
	}
	if err := os.Rename(dir, dir+"-blocked"); err != nil {
		t.Fatal(err)
	}
	// An unchanged catalog notification must retry even before the next probe.
	f.hub.notifyResourceCatalogChange()
	requireEventually(t, 3*time.Second, func() bool { return status().AppliedRevision == first.Manifest.Revision })
	current := filepath.Join(dir, "current.json")
	readDisk := func() targetManifest {
		t.Helper()
		var m targetManifest
		data, err := os.ReadFile(current)
		if err != nil || json.Unmarshal(data, &m) != nil || m.Digest != m.contentDigest() {
			t.Fatalf("invalid manifest on disk: %v", err)
		}
		return m
	}
	confirmed := readDisk()
	if confirmed.Digest != first.Manifest.Digest || confirmed.Revision != first.Manifest.Revision {
		t.Fatal("same projection changed during retry")
	}
	disconnect()
	if got := status(); got.Online || got.AppliedRevision != confirmed.Revision {
		t.Fatal("offline status lost its last confirmation")
	}
	var directory struct{ Targets []targetManifestTarget }
	if err := json.Unmarshal(f.http(t, user, "GET", "/v1/assets/workspace?view=directory", "", 200), &directory); err != nil || len(directory.Targets) != 1 || directory.Targets[0].Mode != confirmed.Target.Mode || directory.Targets[0].KubernetesAvailable == nil {
		t.Fatalf("offline directory lost target identity: %+v (%v)", directory, err)
	}
	// A replacement Agent with empty local storage needs the same version again.
	dir = t.TempDir()
	current = filepath.Join(dir, "current.json")
	disconnect = connectManifestTestAgent(t, f, dir)
	requireEventually(t, 3*time.Second, func() bool {
		_, err := os.Stat(current)
		return err == nil && status().AppliedRevision == confirmed.Revision
	})
	if readDisk().Digest != confirmed.Digest {
		t.Fatal("reconnect changed the projected content")
	}
	disconnect()
	// The same route can next be occupied by an Agent without this protocol.
	// Its first GET must not expose observations or an ACK from the prior peer.
	legacy := dialEnrolledTestAgent(t, f, "ops", "one")
	t.Cleanup(func() { _ = legacy.Close() })
	go serveFakeAgent(t, legacy)
	waitForGatewayAgent(t, f.hub, "ops", "one")
	if old := status(); !old.Online || old.Supported || old.AppliedRevision != 0 || old.LastObservedAt != "" || old.Manifest.Target.Mode != "" {
		t.Fatal("legacy Agent inherited capabilities or facts from the previous connection")
	}
	f.http(t, user, "POST", "/v1/assets/target-manifest", `{"action":"refresh","cluster":"ops","instance":"one"}`, http.StatusConflict)
}

func TestManifestHeartbeatUpdatesOnlineAndPersistedReachability(t *testing.T) {
	stubKubernetesProbe(t, false)
	var reachable atomic.Bool
	probeKubernetesAPI = func(context.Context) bool { return reachable.Load() }
	t.Setenv("PATH", t.TempDir())
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true, AgentLease: 3 * time.Second})
	user := f.user(t, "heartbeat-reader", ScopeGrant{Cluster: "ops", Instance: "one"})
	disconnect := connectManifestTestAgent(t, f, t.TempDir())
	read := func() targetManifestStatus {
		t.Helper()
		var status targetManifestStatus
		if err := json.Unmarshal(f.http(t, user, "GET", "/v1/assets/target-manifest?cluster=ops&instance=one", "", 200), &status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	var first uint64
	requireEventually(t, 3*time.Second, func() bool {
		s := read()
		first = s.AppliedRevision
		return first > 0 && s.Manifest.Target.KubernetesAvailable != nil && !*s.Manifest.Target.KubernetesAvailable
	})
	reachable.Store(true)
	targetRuntimeMu.Lock()
	targetRuntimeExpires = time.Time{}
	targetRuntimeMu.Unlock()
	requireEventually(t, 4*time.Second, func() bool {
		s := read()
		return s.AppliedRevision == first+1 && s.Manifest.Target.KubernetesAvailable != nil && *s.Manifest.Target.KubernetesAvailable
	})
	var online struct{ Targets []GatewayTarget }
	if err := json.Unmarshal(f.http(t, user, "GET", "/v1/targets", "", 200), &online); err != nil || len(online.Targets) != 1 || online.Targets[0].KubernetesAvailable == nil || !*online.Targets[0].KubernetesAvailable {
		t.Fatal("online target list did not reflect the heartbeat")
	}
	disconnect()
	s := read()
	if s.Online || s.Manifest.Target.KubernetesAvailable == nil || !*s.Manifest.Target.KubernetesAvailable {
		t.Fatal("disconnect lost the persisted heartbeat")
	}
}

func TestManifestReplacementRejectsOldApplyWaitingOnSameVolume(t *testing.T) {
	available := false
	target := targetManifestTarget{Cluster: "ops", Instance: "one", Mode: "docker", KubernetesAvailable: &available}
	storage := t.TempDir()
	old := agentResourceSession{target: target, dir: storage}
	newer := targetManifest{Target: target, Revision: 2, Resources: []targetManifestResource{}}
	newer.Digest = newer.contentDigest()
	raw, err := json.Marshal(newer)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the same filesystem lock as the replacement process while the
	// superseded connection submits its older revision.
	dir, err := os.Open(storage)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	stale := newer
	stale.Revision = 1
	oldBytes, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		_, err := old.apply(oldBytes)
		finished <- err
	}()
	<-started
	select {
	case err := <-finished:
		t.Fatalf("old apply bypassed replacement writer: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	if err := writeFileAtomic0600(filepath.Join(storage, "current.json"), raw); err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil || err.Error() != "target_manifest_revision_conflict" {
			t.Fatalf("old apply returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old apply did not finish after release")
	}
	current, _, err := old.current()
	if err != nil || current.Revision != newer.Revision {
		t.Fatal("superseded connection replaced the confirmed disk revision")
	}
}
