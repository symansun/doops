package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayGitHTTPRoutesByClusterAndInstance(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant pull: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, MaxQueuedPerTarget: -1, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	requests := make(chan string, 1)
	go serveFakeGitAgent(t, agentConn, requests)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/git/dev/local/release.git/info/refs?service=git-upload-pack", nil)
	req.SetBasicAuth("doops", userToken.Plaintext)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("git info refs through gateway: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if string(body) != "git-ok" {
		t.Fatalf("unexpected git response body: %q", body)
	}
	select {
	case got := <-requests:
		want := "GET /git/release.git/info/refs?service=git-upload-pack"
		if got != want {
			t.Fatalf("agent received wrong git request:\nwant %s\n got %s", want, got)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not receive git request")
	}
}

func TestGatewayGitHTTPWaitsForShortAgentReconnect(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant pull: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, TargetReconnectGrace: 500 * time.Millisecond, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	reqDone := make(chan *http.Response, 1)
	reqErr := make(chan error, 1)
	go func() {
		url := gatewayGitTestURL(t, ts.URL, "dev", "local", "release", userToken.Plaintext) + "/info/refs?service=git-upload-pack"
		resp, err := http.Get(url)
		if err != nil {
			reqErr <- err
			return
		}
		reqDone <- resp
	}()

	time.Sleep(100 * time.Millisecond)
	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	requests := make(chan string, 1)
	go serveFakeGitAgent(t, agentConn, requests)

	select {
	case err := <-reqErr:
		t.Fatalf("git request failed: %v", err)
	case resp := <-reqDone:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected request to wait for reconnect, got %d: %s", resp.StatusCode, body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("git request did not complete after agent reconnect")
	}
	select {
	case got := <-requests:
		if want := "GET /git/release.git/info/refs?service=git-upload-pack"; got != want {
			t.Fatalf("unexpected git request:\nwant %s\n got %s", want, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnected agent did not receive git request")
	}
}

func TestGatewayGitHTTPEndToEndPushAndFetch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for gateway git e2e test")
	}

	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant push/pull: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: 10 * time.Second, MaxQueuedPerTarget: -1, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentGateway := NewGateway()
	service, err := newGitHTTPHandler(t.TempDir(), "")
	if err != nil {
		t.Fatalf("setup Git service: %v", err)
	}
	agentGateway.gitHandler = http.StripPrefix("/git", service)

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	go agentGateway.ServeGatewayWebSocketConnWithReady(agentConn, "test-agent", nil)
	waitForGatewayAgent(t, hub, "dev", "local")

	repoURL := gatewayGitTestURL(t, ts.URL, "dev", "local", "release", userToken.Plaintext)
	src := t.TempDir()
	runGitCommand(t, src, "init", "-b", "master")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello through gateway git\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGitCommand(t, src, "add", "README.md")
	runGitCommand(t, src, "-c", "user.name=doops", "-c", "user.email=doops@example.com", "commit", "-m", "initial")
	commit := strings.TrimSpace(runGitCommand(t, src, "rev-parse", "HEAD"))

	pushOutput := runGitCommand(t, src, "push", repoURL, "HEAD:master")
	if !strings.Contains(pushOutput, "master") {
		t.Fatalf("push output should mention master branch, got:\n%s", pushOutput)
	}

	remoteRefs := runGitCommand(t, "", "ls-remote", repoURL, "refs/heads/master")
	if !strings.Contains(remoteRefs, commit) {
		t.Fatalf("ls-remote did not return pushed commit %s:\n%s", commit, remoteRefs)
	}
}

func TestGatewayGitHTTPConcurrentPushPullDifferentTargets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for gateway git e2e test")
	}

	hub, ts, tokens := newGatewayGitE2E(t, []gatewayGitE2ETarget{
		{Cluster: "dev-a", Instance: "node-a"},
		{Cluster: "dev-b", Instance: "node-b"},
	})
	defer ts.Close()
	for _, target := range tokens.Targets {
		conn := startGatewayGitTestAgent(t, hub, ts.URL, target.Cluster, target.Instance)
		defer conn.Close()
	}

	errCh := make(chan error, 2)
	for _, target := range tokens.Targets {
		target := target
		go func() {
			errCh <- pushCloneAndVerifyGatewayGit(ts.URL, target.Cluster, target.Instance, "parallel-"+target.Instance, target.UserToken, map[string][]byte{
				"README.md": []byte("hello from " + target.Instance + "\n"),
			})
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
}

func TestGatewayGitHTTPSerialPushDifferentSessionsSameTarget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for gateway git e2e test")
	}

	hub, ts, tokens := newGatewayGitE2E(t, []gatewayGitE2ETarget{
		{Cluster: "dev", Instance: "node"},
	})
	defer ts.Close()
	target := tokens.Targets[0]
	conn := startGatewayGitTestAgent(t, hub, ts.URL, target.Cluster, target.Instance)
	defer conn.Close()

	for _, session := range []string{"parallel-a", "parallel-b"} {
		if err := pushCloneAndVerifyGatewayGit(ts.URL, target.Cluster, target.Instance, session, target.UserToken, map[string][]byte{
			"README.md": []byte("hello from " + session + "\n"),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGatewayGitHTTPLargeFilePushAndPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for gateway git e2e test")
	}

	hub, ts, tokens := newGatewayGitE2E(t, []gatewayGitE2ETarget{{Cluster: "dev-large", Instance: "node-large"}})
	defer ts.Close()
	target := tokens.Targets[0]
	conn := startGatewayGitTestAgent(t, hub, ts.URL, target.Cluster, target.Instance)
	defer conn.Close()

	large := makeDeterministicBytes(24 << 20)
	if err := pushCloneAndVerifyGatewayGit(ts.URL, target.Cluster, target.Instance, "large-file", target.UserToken, map[string][]byte{
		"assets/course-resource.bin": large,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayGitClientCancelReleasesTarget(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant pull: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	requests := make(chan int, 2)
	cancels := make(chan int, 1)
	go serveStallingGitAgent(t, agentConn, requests, cancels)

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstURL := gatewayGitTestURL(t, ts.URL, "dev", "local", "cancelled", userToken.Plaintext) + "/info/refs?service=git-upload-pack"
	firstReq, _ := http.NewRequestWithContext(firstCtx, http.MethodGet, firstURL, nil)
	firstDone := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(firstReq)
		if resp != nil {
			_ = resp.Body.Close()
		}
		firstDone <- err
	}()

	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("first git request did not reach fake agent")
	}
	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled git request did not return")
	}
	select {
	case <-cancels:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway did not cancel the agent-side git handler")
	}

	requireEventually(t, time.Second, func() bool {
		return len(gw.listActiveOperations()) == 0
	})
	secondURL := gatewayGitTestURL(t, ts.URL, "dev", "local", "cancelled", userToken.Plaintext) + "/info/refs?service=git-upload-pack"
	resp, err := http.Get(secondURL)
	if err != nil {
		t.Fatalf("second git request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("target remained busy after cancelled git request: %s", body)
	}
	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("second git request did not reach fake agent")
	}
}

type gatewayGitE2ETarget struct {
	Cluster  string
	Instance string
}

type gatewayGitE2EToken struct {
	Cluster   string
	Instance  string
	UserToken string
}

type gatewayGitE2ETokens struct {
	Targets []gatewayGitE2EToken
}

func newGatewayGitE2E(t *testing.T, targets []gatewayGitE2ETarget) (*GatewayHub, *httptest.Server, gatewayGitE2ETokens) {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: 30 * time.Second, MaxQueuedPerTarget: -1, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)

	out := gatewayGitE2ETokens{Targets: make([]gatewayGitE2EToken, 0, len(targets))}
	for idx, target := range targets {
		user, err := store.CreateUser(fmt.Sprintf("user-%d", idx))
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		userToken, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "git-e2e"})
		if err != nil {
			t.Fatalf("create user token: %v", err)
		}
		if err := store.GrantUser(user.ID, ScopeGrant{Cluster: target.Cluster, Instance: target.Instance}); err != nil {
			t.Fatalf("grant push/pull: %v", err)
		}
		out.Targets = append(out.Targets, gatewayGitE2EToken{
			Cluster:   target.Cluster,
			Instance:  target.Instance,
			UserToken: userToken.Plaintext,
		})
	}
	return hub, ts, out
}

func startGatewayGitTestAgent(t *testing.T, hub *GatewayHub, serverURL, cluster, instance string) *websocket.Conn {
	t.Helper()
	agentGateway := NewGateway()
	service, err := newGitHTTPHandler(t.TempDir(), "")
	if err != nil {
		t.Fatalf("setup Git service: %v", err)
	}
	agentGateway.gitHandler = http.StripPrefix("/git", service)

	conn := dialFakeGatewayAgent(t, serverURL, cluster, instance)
	go agentGateway.ServeGatewayWebSocketConnWithReady(conn, "test-agent", nil)
	waitForGatewayAgent(t, hub, cluster, instance)
	return conn
}

func gatewayGitTestURL(t *testing.T, serverURL, cluster, instance, session, token string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	u.User = url.UserPassword("doops", token)
	u.Path = "/v1/git/" + cluster + "/" + instance + "/" + session + ".git"
	u.RawQuery = ""
	return u.String()
}

func pushCloneAndVerifyGatewayGit(serverURL, cluster, instance, session, token string, files map[string][]byte) error {
	src, err := os.MkdirTemp("", "doops-git-src-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(src)
	for name, content := range files {
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			return err
		}
	}
	if _, err := runGitCommandE(src, "init", "-b", "master"); err != nil {
		return err
	}
	if _, err := runGitCommandE(src, "add", "."); err != nil {
		return err
	}
	if _, err := runGitCommandE(src, "-c", "user.name=doops", "-c", "user.email=doops@example.com", "commit", "-m", "sync"); err != nil {
		return err
	}
	commit, err := runGitCommandE(src, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	repoURL, err := gatewayGitURL(serverURL, cluster, instance, session, token)
	if err != nil {
		return err
	}
	if _, err := runGitCommandE(src, "push", repoURL, "HEAD:master"); err != nil {
		return err
	}
	refs, err := runGitCommandE("", "ls-remote", repoURL, "refs/heads/master")
	if err != nil {
		return err
	}
	if !strings.Contains(refs, strings.TrimSpace(commit)) {
		return fmt.Errorf("remote refs do not contain pushed commit %s: %s", strings.TrimSpace(commit), refs)
	}
	dest, err := os.MkdirTemp("", "doops-git-clone-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dest)
	if _, err := runGitCommandE("", "clone", "--quiet", "--branch", "master", repoURL, dest); err != nil {
		return err
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			return err
		}
		if sha256.Sum256(got) != sha256.Sum256(want) {
			return fmt.Errorf("cloned file checksum mismatch: %s", name)
		}
	}
	return nil
}

func gatewayGitURL(serverURL, cluster, instance, session, token string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword("doops", token)
	u.Path = "/v1/git/" + cluster + "/" + instance + "/" + session + ".git"
	u.RawQuery = ""
	return u.String(), nil
}

func runGitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := runGitCommandE(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func runGitCommandE(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func makeDeterministicBytes(size int) []byte {
	out := make([]byte, size)
	rng := rand.New(rand.NewSource(42))
	_, _ = rng.Read(out)
	return out
}

func serveFakeGitAgent(t *testing.T, conn *websocket.Conn, requests chan<- string) {
	t.Helper()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  gatewayTestInitializeResult(""),
			})
		case "git/http":
			params, _ := msg["params"].(map[string]interface{})
			requests <- fmt.Sprintf("%s %s?%s", params["method"], params["path"], params["raw_query"])
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"method":  "git/response",
				"params": map[string]interface{}{
					"status":  200,
					"headers": map[string][]string{"Content-Type": {"application/x-git-upload-pack-advertisement"}},
				},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"method":  "git/body",
				"params":  map[string]interface{}{"data_b64": "Z2l0LW9r", "eof": true},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  map[string]interface{}{"ok": true},
			})
		}
	}
}

func serveStallingGitAgent(t *testing.T, conn *websocket.Conn, requests chan<- int, cancels chan<- int) {
	t.Helper()
	var count int
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  gatewayTestInitializeResult(""),
			})
		case "git/http":
			count++
			requests <- 1
			if count == 1 {
				continue
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"method":  "git/response",
				"params": map[string]interface{}{
					"status":  200,
					"headers": map[string][]string{"Content-Type": {"application/x-git-upload-pack-advertisement"}},
				},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"method":  "git/body",
				"params":  map[string]interface{}{"data_b64": "Z2l0LW9r", "eof": true},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  map[string]interface{}{"ok": true},
			})
		case "git/cancel":
			params, _ := msg["params"].(map[string]interface{})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": msg["id"],
				"result": map[string]interface{}{"canceled": true},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": params["id"],
				"error": map[string]interface{}{"code": -32800, "message": "operation canceled"},
			})
			cancels <- 1
		}
	}
}
