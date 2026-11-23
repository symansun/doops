package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	mcpSyncTool    = "doops_mcp_sync"
	mcpInspectTool = "doops_mcp_inspect"

	mcpProbeTimeout = 12 * time.Second
)

var (
	mcpSlugPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)
	mcpEnvKeyPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	mcpHeaderKeyCheck = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

type mcpToolMeta struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type agentMCPServerEntry struct {
	Name            string            `json:"name"`
	Transport       string            `json:"transport"` // stdio | http | sse
	Command         string            `json:"command,omitempty"`
	Args            []string          `json:"args,omitempty"`
	URL             string            `json:"url,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	AutoStart       bool              `json:"auto_start"`
	ToolTimeoutSecs int               `json:"tool_timeout_secs,omitempty"`
	ProbedTools     []string          `json:"probed_tools,omitempty"`
}

type mcpSyncParams struct {
	SessionID string                `json:"session_id"`
	Servers   []agentMCPServerEntry `json:"servers"`
	Probe     bool                  `json:"probe,omitempty"`
}

type mcpInspectParams struct {
	SessionID  string               `json:"session_id"`
	ServerName string               `json:"server_name,omitempty"`
	Probe      bool                 `json:"probe,omitempty"`
	AdHoc      *agentMCPServerEntry `json:"ad_hoc,omitempty"`
}

type mcpServerInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type mcpInspectServerResult struct {
	Name            string            `json:"name"`
	Builtin         bool              `json:"builtin"`
	Transport       string            `json:"transport"`
	Command         string            `json:"command,omitempty"`
	Args            []string          `json:"args,omitempty"`
	URL             string            `json:"url,omitempty"`
	AutoStart       bool              `json:"auto_start"`
	ToolTimeoutSecs int               `json:"tool_timeout_secs,omitempty"`
	Status          string            `json:"status"` // ok | error | configured
	Error           string            `json:"error,omitempty"`
	LatencyMs       int64             `json:"latency_ms,omitempty"`
	ServerInfo      *mcpServerInfo    `json:"server_info,omitempty"`
	Tools           []mcpToolMeta     `json:"tools"`
	EnvKeys         []string          `json:"env_keys,omitempty"`
	HeaderKeys      []string          `json:"header_keys,omitempty"`
	Headers         map[string]string `json:"-"`
}

func doagentCustomMCPPath() string {
	return filepath.Join(filepath.Dir(doagentSettingsPath()), "doops-mcp-servers.json")
}

func readCustomMCPServers() []agentMCPServerEntry {
	data, err := os.ReadFile(doagentCustomMCPPath())
	if err != nil {
		return nil
	}
	var entries []agentMCPServerEntry
	if json.Unmarshal(data, &entries) != nil {
		return nil
	}
	out := make([]agentMCPServerEntry, 0, len(entries))
	seen := map[string]bool{"doops": true, "dosql": true}
	for _, e := range entries {
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if !mcpSlugPattern.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		e.Name = name
		out = append(out, e)
	}
	return out
}

func writeCustomMCPServers(entries []agentMCPServerEntry) error {
	clean := make([]agentMCPServerEntry, 0, len(entries))
	seen := map[string]bool{"doops": true, "dosql": true}
	for _, e := range entries {
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if !mcpSlugPattern.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		e.Name = name
		if e.ToolTimeoutSecs <= 0 {
			e.ToolTimeoutSecs = 120
		}
		clean = append(clean, e)
	}
	encoded, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic0600(doagentCustomMCPPath(), append(encoded, '\n'))
}

func mcpEntryToDoagentSetting(e agentMCPServerEntry) map[string]any {
	timeout := e.ToolTimeoutSecs
	if timeout <= 0 {
		timeout = 120
	}
	env := e.Env
	if env == nil {
		env = map[string]string{}
	}
	entry := map[string]any{
		"name":              e.Name,
		"capabilities":      []string{"tools"},
		"auto_start":        e.AutoStart,
		"tool_timeout_secs": timeout,
		"required":          false,
		"status":            "Unknown",
	}
	if e.Transport == "http" || e.Transport == "sse" {
		entry["transport"] = e.Transport
		entry["url"] = e.URL
		if len(e.Headers) > 0 {
			entry["headers"] = e.Headers
		}
	} else {
		args := e.Args
		if args == nil {
			args = []string{}
		}
		entry["command"] = e.Command
		entry["args"] = args
		entry["env"] = env
		entry["cwd"] = nil
	}
	return entry
}

func effectiveDoagentMCPServers() []map[string]any {
	out := make([]map[string]any, 0, len(doagentRuntimeMCPServers)+4)
	out = append(out, doagentRuntimeMCPServers...)
	for _, custom := range readCustomMCPServers() {
		out = append(out, mcpEntryToDoagentSetting(custom))
	}
	return out
}

func customSyncedMCPAllowedTools() []string {
	custom := readCustomMCPServers()
	if len(custom) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, srv := range custom {
		for _, tool := range srv.ProbedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			full := "mcp_" + srv.Name + "_" + tool
			if !seen[full] {
				seen[full] = true
				out = append(out, full)
			}
		}
	}
	sort.Strings(out)
	return out
}

func effectiveDoagentPermissions() map[string]any {
	allow := []string{"mcp_doops_k8s_read", "mcp_dosql_read"}
	allow = append(allow, customSyncedMCPAllowedTools()...)
	return map[string]any{
		"default_mode": "restricted",
		"presets": map[string]any{
			"always_allow": allow,
			"always_deny":  []string{},
		},
		"timeout_seconds": 30,
	}
}

func validateMCPServerEntry(e *agentMCPServerEntry) error {
	e.Name = strings.ToLower(strings.TrimSpace(e.Name))
	if !mcpSlugPattern.MatchString(e.Name) {
		return errors.New("invalid mcp server name")
	}
	e.Transport = strings.ToLower(strings.TrimSpace(e.Transport))
	if e.Transport == "" {
		e.Transport = "stdio"
	}
	switch e.Transport {
	case "stdio":
		e.Command = strings.TrimSpace(e.Command)
		if e.Command == "" || len(e.Command) > 512 || strings.ContainsAny(e.Command, "\r\n\x00") {
			return errors.New("stdio mcp server requires a valid command")
		}
		if len(e.Args) > 64 {
			return errors.New("too many mcp server args")
		}
		for _, a := range e.Args {
			if len(a) > 1024 || strings.ContainsRune(a, '\x00') {
				return errors.New("invalid mcp server arg")
			}
		}
	case "http", "sse":
		e.URL = strings.TrimSpace(e.URL)
		if (!strings.HasPrefix(e.URL, "http://") && !strings.HasPrefix(e.URL, "https://")) || len(e.URL) > 1024 || strings.ContainsAny(e.URL, " \r\n\x00") {
			return errors.New("http/sse mcp server requires a valid http(s) url")
		}
	default:
		return errors.New("unsupported mcp transport (use stdio, http, or sse)")
	}
	if len(e.Env) > 32 {
		return errors.New("too many env vars")
	}
	for k, v := range e.Env {
		if !mcpEnvKeyPattern.MatchString(k) || len(v) > 2048 || strings.ContainsRune(v, '\x00') {
			return fmt.Errorf("invalid env key/value: %s", k)
		}
	}
	if len(e.Headers) > 24 {
		return errors.New("too many http headers")
	}
	for k, v := range e.Headers {
		if !mcpHeaderKeyCheck.MatchString(k) || len(v) > 2048 || strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("invalid header: %s", k)
		}
	}
	if e.ToolTimeoutSecs <= 0 {
		e.ToolTimeoutSecs = 120
	}
	if e.ToolTimeoutSecs > 1800 {
		e.ToolTimeoutSecs = 1800
	}
	return nil
}

// probeMCPServer performs a real MCP JSON-RPC 2.0 initialize + tools/list handshake
// over stdio or HTTP/SSE and returns real serverInfo, tool definitions, and latency.
func probeMCPServer(ctx context.Context, entry agentMCPServerEntry) mcpInspectServerResult {
	started := time.Now()
	res := mcpInspectServerResult{
		Name:            entry.Name,
		Builtin:         entry.Name == "doops" || entry.Name == "dosql",
		Transport:       entry.Transport,
		Command:         entry.Command,
		Args:            entry.Args,
		URL:             entry.URL,
		AutoStart:       entry.AutoStart,
		ToolTimeoutSecs: entry.ToolTimeoutSecs,
		Tools:           []mcpToolMeta{},
		EnvKeys:         sortedMapKeys(entry.Env),
		HeaderKeys:      sortedMapKeys(entry.Headers),
	}
	if res.Transport == "" {
		res.Transport = "stdio"
	}
	probeCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()

	var (
		info  *mcpServerInfo
		tools []mcpToolMeta
		err   error
	)
	if res.Transport == "http" || res.Transport == "sse" {
		info, tools, err = probeHTTPMCP(probeCtx, entry)
	} else {
		info, tools, err = probeStdioMCP(probeCtx, entry)
	}
	res.LatencyMs = time.Since(started).Milliseconds()
	if err != nil {
		res.Status = "error"
		res.Error = trimTail(sanitizeSecretText(err.Error()), 300)
		return res
	}
	res.Status = "ok"
	res.ServerInfo = info
	if tools != nil {
		res.Tools = tools
	}
	return res
}

type mcpRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func parseMCPInitializeResult(raw json.RawMessage) *mcpServerInfo {
	var parsed struct {
		ServerInfo *mcpServerInfo `json:"serverInfo"`
	}
	if json.Unmarshal(raw, &parsed) == nil && parsed.ServerInfo != nil {
		return parsed.ServerInfo
	}
	return nil
}

func parseMCPToolsListResult(raw json.RawMessage) ([]mcpToolMeta, error) {
	var parsed struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]mcpToolMeta, 0, len(parsed.Tools))
	for _, t := range parsed.Tools {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		out = append(out, mcpToolMeta{
			Name:        t.Name,
			Description: trimTail(strings.TrimSpace(t.Description), 300),
			InputSchema: t.InputSchema,
		})
	}
	return out, nil
}

func probeStdioMCP(ctx context.Context, entry agentMCPServerEntry) (*mcpServerInfo, []mcpToolMeta, error) {
	cmd := exec.CommandContext(ctx, entry.Command, entry.Args...)
	if len(entry.Env) > 0 {
		env := append([]string(nil), os.Environ()...)
		for k, v := range entry.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"doops-agent","version":"1.0"}}}` + "\n"
	if _, err := io.WriteString(stdin, initReq); err != nil {
		return nil, nil, err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	readByID := func(wantID string) (mcpRPCMessage, error) {
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || !strings.HasPrefix(line, "{") {
				continue
			}
			var msg mcpRPCMessage
			if json.Unmarshal([]byte(line), &msg) != nil {
				continue
			}
			if strings.TrimSpace(string(msg.ID)) == wantID {
				if msg.Error != nil {
					return msg, fmt.Errorf("mcp rpc error %d: %s", msg.Error.Code, msg.Error.Message)
				}
				return msg, nil
			}
		}
		if err := scanner.Err(); err != nil {
			return mcpRPCMessage{}, err
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return mcpRPCMessage{}, fmt.Errorf("mcp stdio closed before response %s: %s", wantID, trimTail(errMsg, 200))
		}
		return mcpRPCMessage{}, fmt.Errorf("mcp stdio closed before response %s", wantID)
	}

	initResp, err := readByID("1")
	if err != nil {
		return nil, nil, err
	}
	info := parseMCPInitializeResult(initResp.Result)

	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`+"\n"); err != nil {
		return info, nil, err
	}
	toolsResp, err := readByID("2")
	if err != nil {
		return info, nil, err
	}
	tools, err := parseMCPToolsListResult(toolsResp.Result)
	return info, tools, err
}

func probeHTTPMCP(ctx context.Context, entry agentMCPServerEntry) (*mcpServerInfo, []mcpToolMeta, error) {
	client := &http.Client{Timeout: mcpProbeTimeout}
	sessionHeader := ""

	postRPC := func(payload string, expectResponse bool) (mcpRPCMessage, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, entry.URL, strings.NewReader(payload))
		if err != nil {
			return mcpRPCMessage{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionHeader != "" {
			req.Header.Set("Mcp-Session-Id", sessionHeader)
		}
		for k, v := range entry.Headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return mcpRPCMessage{}, err
		}
		defer resp.Body.Close()
		if sid := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sid != "" {
			sessionHeader = sid
		}
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return mcpRPCMessage{}, fmt.Errorf("http %d: %s", resp.StatusCode, trimTail(strings.TrimSpace(string(bodyBytes)), 200))
		}
		if !expectResponse {
			return mcpRPCMessage{}, nil
		}
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		var candidate []byte
		if strings.Contains(ct, "text/event-stream") {
			for _, line := range strings.Split(string(bodyBytes), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "data:") {
					d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					if strings.HasPrefix(d, "{") {
						candidate = []byte(d)
						break
					}
				}
			}
		} else {
			candidate = bytes.TrimSpace(bodyBytes)
		}
		if len(candidate) == 0 {
			return mcpRPCMessage{}, errors.New("empty mcp http response")
		}
		var msg mcpRPCMessage
		if err := json.Unmarshal(candidate, &msg); err != nil {
			return mcpRPCMessage{}, fmt.Errorf("invalid mcp json response: %w", err)
		}
		if msg.Error != nil {
			return msg, fmt.Errorf("mcp rpc error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		return msg, nil
	}

	initResp, err := postRPC(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"doops-agent","version":"1.0"}}}`, true)
	if err != nil {
		return nil, nil, err
	}
	info := parseMCPInitializeResult(initResp.Result)
	_, _ = postRPC(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, false)
	toolsResp, err := postRPC(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, true)
	if err != nil {
		return info, nil, err
	}
	tools, err := parseMCPToolsListResult(toolsResp.Result)
	return info, tools, err
}

func sortedMapKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func readActiveSettingsMCPServers() []agentMCPServerEntry {
	out := make([]agentMCPServerEntry, 0, 4)
	seen := map[string]bool{}
	if data, err := os.ReadFile(doagentSettingsPath()); err == nil {
		var settings struct {
			MCPServers []struct {
				Name            string            `json:"name"`
				Transport       string            `json:"transport"`
				Command         string            `json:"command"`
				Args            []string          `json:"args"`
				URL             string            `json:"url"`
				Env             map[string]string `json:"env"`
				Headers         map[string]string `json:"headers"`
				AutoStart       bool              `json:"auto_start"`
				ToolTimeoutSecs int               `json:"tool_timeout_secs"`
			} `json:"mcp_servers"`
		}
		if json.Unmarshal(data, &settings) == nil {
			for _, s := range settings.MCPServers {
				name := strings.ToLower(strings.TrimSpace(s.Name))
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				tr := strings.ToLower(strings.TrimSpace(s.Transport))
				if tr == "" {
					if s.URL != "" {
						tr = "http"
					} else {
						tr = "stdio"
					}
				}
				out = append(out, agentMCPServerEntry{
					Name:            name,
					Transport:       tr,
					Command:         s.Command,
					Args:            s.Args,
					URL:             s.URL,
					Env:             s.Env,
					Headers:         s.Headers,
					AutoStart:       s.AutoStart,
					ToolTimeoutSecs: s.ToolTimeoutSecs,
				})
			}
		}
	}
	if len(out) == 0 {
		for _, domain := range []string{"doops", "dosql"} {
			out = append(out, agentMCPServerEntry{
				Name:            domain,
				Transport:       "stdio",
				Command:         "/app/doops-agent",
				Args:            []string{"mcp-stdio", "--domain", domain},
				AutoStart:       true,
				ToolTimeoutSecs: doopsMCPToolTimeoutSeconds,
			})
		}
	}
	return out
}

func (gw *Gateway) handleToolMCPSync(call toolInvocation) {
	reqID, writeJSON := call.reqID, call.writeJSON
	var params mcpSyncParams
	if err := json.Unmarshal(call.args, &params); err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid mcp sync params"))
		return
	}
	clean := make([]agentMCPServerEntry, 0, len(params.Servers))
	seen := map[string]bool{"doops": true, "dosql": true}
	for i := range params.Servers {
		entry := params.Servers[i]
		if err := validateMCPServerEntry(&entry); err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
			return
		}
		if seen[entry.Name] {
			continue
		}
		seen[entry.Name] = true
		clean = append(clean, entry)
	}

	var probeResults []mcpInspectServerResult
	if params.Probe && len(clean) > 0 {
		probeResults = make([]mcpInspectServerResult, len(clean))
		var wg sync.WaitGroup
		for i, entry := range clean {
			wg.Add(1)
			go func(idx int, e agentMCPServerEntry) {
				defer wg.Done()
				r := probeMCPServer(call.ctx, e)
				probeResults[idx] = r
				if r.Status == "ok" && len(r.Tools) > 0 {
					names := make([]string, 0, len(r.Tools))
					for _, t := range r.Tools {
						names = append(names, t.Name)
					}
					clean[idx].ProbedTools = names
				}
			}(i, entry)
		}
		wg.Wait()
	}

	modelProviderSettingsMu.Lock()
	defer modelProviderSettingsMu.Unlock()
	if err := writeCustomMCPServers(clean); err != nil {
		writeJSON(buildToolErrorResponse(reqID, "mcp_sync_write_failed: "+err.Error()))
		return
	}
	// Preserve existing model/provider fields untouched when updating mcp_servers.
	existingModel := map[string]any{}
	if data, err := os.ReadFile(doagentSettingsPath()); err == nil {
		var existing map[string]any
		if json.Unmarshal(data, &existing) == nil {
			for _, key := range modelSettingsKeys {
				if val, ok := existing[key]; ok {
					existingModel[key] = val
				}
			}
		}
	}
	if err := writeDoagentSettings(existingModel); err != nil {
		writeJSON(buildToolErrorResponse(reqID, "mcp_settings_update_failed: "+err.Error()))
		return
	}

	out, _ := json.Marshal(map[string]any{
		"synced_count":  len(clean),
		"total_servers": len(doagentRuntimeMCPServers) + len(clean),
		"settings_path": doagentSettingsPath(),
		"probe_results": probeResults,
	})
	writeJSON(buildSuccessResponse(reqID, string(out)))
}

func (gw *Gateway) handleToolMCPInspect(call toolInvocation) {
	reqID, writeJSON := call.reqID, call.writeJSON
	var params mcpInspectParams
	if err := json.Unmarshal(call.args, &params); err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid mcp inspect params"))
		return
	}

	if params.AdHoc != nil {
		entry := *params.AdHoc
		if err := validateMCPServerEntry(&entry); err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
			return
		}
		res := probeMCPServer(call.ctx, entry)
		out, _ := json.Marshal(map[string]any{
			"settings_path": doagentSettingsPath(),
			"servers":       []mcpInspectServerResult{res},
		})
		writeJSON(buildSuccessResponse(reqID, string(out)))
		return
	}

	entries := readActiveSettingsMCPServers()
	wantName := strings.ToLower(strings.TrimSpace(params.ServerName))
	if wantName != "" {
		filtered := make([]agentMCPServerEntry, 0, 1)
		for _, e := range entries {
			if e.Name == wantName {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	results := make([]mcpInspectServerResult, len(entries))
	if params.Probe {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for i, entry := range entries {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, e agentMCPServerEntry) {
				defer wg.Done()
				defer func() { <-sem }()
				results[idx] = probeMCPServer(call.ctx, e)
			}(i, entry)
		}
		wg.Wait()
	} else {
		for i, e := range entries {
			results[i] = mcpInspectServerResult{
				Name:            e.Name,
				Builtin:         e.Name == "doops" || e.Name == "dosql",
				Transport:       e.Transport,
				Command:         e.Command,
				Args:            e.Args,
				URL:             e.URL,
				AutoStart:       e.AutoStart,
				ToolTimeoutSecs: e.ToolTimeoutSecs,
				Status:          "configured",
				Tools:           []mcpToolMeta{},
				EnvKeys:         sortedMapKeys(e.Env),
				HeaderKeys:      sortedMapKeys(e.Headers),
			}
		}
	}

	out, _ := json.Marshal(map[string]any{
		"settings_path": doagentSettingsPath(),
		"servers":       results,
	})
	writeJSON(buildSuccessResponse(reqID, string(out)))
}
