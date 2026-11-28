package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

func configuredDoagentURL() string {
	if baseURL := strings.TrimSpace(os.Getenv("DO_AGENT_URL")); baseURL != "" {
		return baseURL
	}
	port := strings.TrimSpace(os.Getenv("DO_AGENT_PORT"))
	if port == "" {
		port = "9000"
	}
	return "http://127.0.0.1:" + port
}

func ensureDoagentAvailable(baseURL string) error {
	if doagentTCPReady(baseURL, 500*time.Millisecond) {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid DO_AGENT_URL %q: %w", baseURL, err)
	}
	host := parsed.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "" {
		return fmt.Errorf("%s is not reachable", baseURL)
	}
	port := parsed.Port()
	if port == "" {
		port = "9000"
	}
	shell, err := exec.LookPath("/app/doops-shell")
	if err != nil {
		return fmt.Errorf("required doagent shell: %w", err)
	}
	if err := os.MkdirAll("/root/ws", 0755); err != nil {
		return fmt.Errorf("create /root/ws: %w", err)
	}
	cmd := exec.Command("/usr/local/bin/do-agent", "acp-http", "--port", port, "--cwd", "/root/ws")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = append(os.Environ(), "DO_AGENT_BASH_WAIT_FOR_EXIT=1", "DO_AGENT_SHELL="+shell, "DO_AGENT_MUTATION_AUTHORITY_SECRET="+processMutationAuthoritySecret)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start do-agent acp-http: %w", err)
	}
	log.Printf("🤖 started doagent ACP HTTP on demand: pid=%d port=%s", cmd.Process.Pid, port)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for i := 0; i < 20; i++ {
		if doagentTCPReady(baseURL, 500*time.Millisecond) {
			return nil
		}
		select {
		case err := <-exited:
			if err != nil {
				return fmt.Errorf("do-agent exited before listening: %w", err)
			}
			return errors.New("do-agent exited before listening")
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("do-agent did not listen after startup")
}

func doagentTCPReady(baseURL string, timeout time.Duration) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "9000"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// doagentRPC 向本地 doagent ACP HTTP 服务发送 JSON-RPC 请求并返回响应。
func doagentRPC(baseURL string, payload map[string]interface{}) (map[string]interface{}, error) {
	return doagentRPCContext(context.Background(), baseURL, payload)
}

func doagentRPCContext(ctx context.Context, baseURL string, payload map[string]interface{}) (map[string]interface{}, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /rpc: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response (status=%d): %w", resp.StatusCode, err)
	}

	if rpcErr, ok := result["error"]; ok && rpcErr != nil {
		return result, fmt.Errorf("RPC error: %v", rpcErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RPC HTTP status %d", resp.StatusCode)
	}

	return result, nil
}

const doagentCancellationTimeout = 10 * time.Second

func cancelDoagentPrompt(ctx context.Context, baseURL, sessionID string, done <-chan struct{}, terminalSeen *atomic.Bool, timeout time.Duration) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	// session/cancel acknowledges a request, not termination. Leave SSE open
	// until the matching turn_finished, even if the ACK says not_running.
	_, _ = doagentRPCContext(cleanupCtx, baseURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "cancel-" + sessionID,
		"method":  "session/cancel",
		"params":  map[string]interface{}{"sessionId": sessionID},
	})
	if done != nil {
		select {
		case <-done:
		case <-cleanupCtx.Done():
		}
	}
	if terminalSeen.Load() {
		return nil
	}
	return errExecutionOutcomeUnknown
}
