package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	terminalAIRequestTimeout    = 30 * time.Second
	terminalAIMaxRequestBytes   = 128 * 1024
	terminalAIMaxResponseBytes  = 256 * 1024
	terminalAIMaxQuestionRunes  = 4000
	terminalAIMaxContextRunes   = 32000
	terminalAIMaxCommands       = 4
	terminalAIMaxExplanationLen = 1200
)

// terminalAIAllowLoopbackForTest lets unit tests point modelProvider baseURL at
// httptest.NewServer without relaxing production HTTPS enforcement.
var terminalAIAllowLoopbackForTest = false

// terminalAIHTTPTransport can be overridden in unit tests if needed.
var terminalAIHTTPTransport http.RoundTripper

type terminalAIRequest struct {
	Cluster       string `json:"cluster"`
	Instance      string `json:"instance"`
	SessionID     string `json:"session_id"`
	Question      string `json:"question"`
	Action        string `json:"action,omitempty"`
	Cwd           string `json:"cwd,omitempty"`
	ContextPrompt string `json:"context_prompt,omitempty"`
}

type terminalAICommand struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

// UnmarshalJSON accepts both {"command":"...","description":"..."} objects and
// plain command strings (some lightweight models emit commands as string arrays).
func (c *terminalAICommand) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '"' {
		var plain string
		if err := json.Unmarshal(trimmed, &plain); err != nil {
			return err
		}
		c.Command = plain
		c.Description = ""
		return nil
	}
	type alias terminalAICommand
	var obj alias
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return err
	}
	*c = terminalAICommand(obj)
	return nil
}

type terminalAIUsage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	TotalTokens  int64 `json:"total_tokens,omitempty"`
}

type terminalAIResponse struct {
	OK          bool                `json:"ok"`
	Model       string              `json:"model"`
	Provider    string              `json:"provider"`
	Explanation string              `json:"explanation"`
	Commands    []terminalAICommand `json:"commands"`
	Usage       *terminalAIUsage    `json:"usage,omitempty"`
}

type terminalAIProviderEndpoint struct {
	ProviderID string
	ModelID    string
	FullModel  string
	Kind       string // "openai" | "anthropic"
	URL        string
	APIKey     string
}

// HandleTerminalAI serves single-shot terminal AI command generation / explanation
// directly from the Gateway using the target's active modelProvider credential,
// without creating or polluting a multi-turn doagent assistant session.
func (h *GatewayHub) HandleTerminalAI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}

	var req terminalAIRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, terminalAIMaxRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeDoopsError(w, http.StatusBadRequest, "", doopsErrorResponse{
			Error:   "invalid_request",
			Code:    "invalid_request",
			Message: "请求体格式无效",
		})
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeDoopsError(w, http.StatusBadRequest, "", doopsErrorResponse{
			Error:   "invalid_request",
			Code:    "invalid_request",
			Message: "请求体包含多余内容",
		})
		return
	}
	if err := validateTerminalAIRequest(&req); err != nil {
		writeDoopsError(w, http.StatusBadRequest, "", doopsErrorResponse{
			Error:   "invalid_request",
			Code:    "invalid_request",
			Message: err.Error(),
		})
		return
	}

	if !h.store.UserCanUseTarget(auth.UserID, req.Cluster, req.Instance) {
		writeDoopsError(w, http.StatusForbidden, "", doopsErrorResponse{
			Error:   "forbidden",
			Code:    "forbidden",
			Message: "当前账号无权访问该目标",
		})
		return
	}

	bindings, err := h.store.ActiveModelProviderBindings(req.Cluster, req.Instance)
	if err != nil {
		writeDoopsError(w, http.StatusInternalServerError, "", doopsErrorResponse{
			Error:   "model_provider_lookup_failed",
			Code:    "model_provider_lookup_failed",
			Message: "查询目标模型凭据失败",
		})
		return
	}
	switch len(bindings) {
	case 0:
		writeDoopsError(w, http.StatusPreconditionFailed, "", doopsErrorResponse{
			Error:      "model_provider_not_configured",
			Code:       "model_provider_not_configured",
			Message:    "当前目标未绑定可用的模型服务凭据",
			NextAction: "请在「凭据」中为该目标绑定并激活 modelProvider 凭据",
		})
		return
	case 1:
	default:
		writeDoopsError(w, http.StatusConflict, "", doopsErrorResponse{
			Error:      "model_provider_ambiguous",
			Code:       "model_provider_ambiguous",
			Message:    "当前目标绑定了多个激活的模型服务凭据",
			NextAction: "请仅保留一个激活的 modelProvider 绑定",
		})
		return
	}

	binding := bindings[0]
	version, err := h.store.ActiveCredentialVersion(binding.Resource.ID)
	if err != nil {
		writeDoopsError(w, http.StatusPreconditionFailed, "", doopsErrorResponse{
			Error:      "model_provider_not_promoted",
			Code:       "model_provider_not_promoted",
			Message:    "当前目标绑定的模型服务凭据尚未激活版本",
			NextAction: "请在「凭据」中完成验证并激活版本",
		})
		return
	}

	// Fail closed if audit persistence is unavailable before decrypting any credential.
	auditID, err := h.store.StartAudit(AuditEvent{
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		Cluster:        req.Cluster,
		Instance:       req.Instance,
		CredentialID:   binding.Resource.ID,
		VersionID:      version.ID,
		GrantID:        binding.Grant.ID,
		Action:         GatewayAction("terminal:ai"),
		Session:        req.SessionID,
		CommandSummary: summarizeTerminalAIQuestion(req.Action, req.Question),
	})
	if err != nil {
		writeDoopsError(w, http.StatusInternalServerError, "", doopsErrorResponse{
			Error:   "credential_audit_unavailable",
			Code:    "credential_audit_unavailable",
			Message: "审计记录不可用，已拒绝调用模型凭据",
		})
		return
	}

	finishErr := func(status int, code, msg string) {
		_ = h.store.FinishAudit(auditID, AuditFinish{
			Status: "error",
			Error:  code,
		})
		writeDoopsError(w, status, "", doopsErrorResponse{
			Error:   code,
			Code:    code,
			Message: msg,
		})
	}

	rawPayload, err := h.store.ResolveCredentialPayload(binding.Resource.ID, version.ID)
	if err != nil {
		finishErr(http.StatusInternalServerError, "credential_decrypt_failed", "模型凭据解密失败")
		return
	}
	defer clear(rawPayload)

	endpoint, err := resolveTerminalAIProviderEndpoint(rawPayload)
	if err != nil {
		finishErr(http.StatusInternalServerError, "model_provider_invalid", "模型服务凭据配置无效")
		return
	}
	defer func() { endpoint.APIKey = "" }()

	ctx, cancel := context.WithTimeout(r.Context(), terminalAIRequestTimeout)
	defer cancel()

	rawText, usage, statusCode, errCode, errMsg := invokeTerminalAIUpstream(ctx, endpoint, req)
	if errCode != "" {
		_ = h.store.FinishAudit(auditID, AuditFinish{
			Status: "error",
			Model:  endpoint.FullModel,
			Error:  errCode,
		})
		writeDoopsError(w, statusCode, "", doopsErrorResponse{
			Error:   errCode,
			Code:    errCode,
			Message: errMsg,
		})
		return
	}

	explanation, commands := parseTerminalAIOutput(rawText)
	if explanation == "" && len(commands) == 0 {
		_ = h.store.FinishAudit(auditID, AuditFinish{
			Status: "error",
			Model:  endpoint.FullModel,
			Error:  "terminal_ai_empty_response",
		})
		writeDoopsError(w, http.StatusBadGateway, "", doopsErrorResponse{
			Error:   "terminal_ai_empty_response",
			Code:    "terminal_ai_empty_response",
			Message: "模型未返回有效回答，请重试",
		})
		return
	}

	_ = h.store.FinishAudit(auditID, AuditFinish{
		Status:       "success",
		Model:        endpoint.FullModel,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		Tail:         fmt.Sprintf("commands=%d", len(commands)),
	})

	resp := terminalAIResponse{
		OK:          true,
		Model:       endpoint.FullModel,
		Provider:    endpoint.ProviderID,
		Explanation: explanation,
		Commands:    commands,
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.TotalTokens > 0 {
		u := usage
		resp.Usage = &u
	}
	writeJSONHTTP(w, resp)
}

func validateTerminalAIRequest(req *terminalAIRequest) error {
	req.Cluster = strings.TrimSpace(req.Cluster)
	req.Instance = strings.TrimSpace(req.Instance)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Question = strings.TrimSpace(req.Question)
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Cwd = strings.TrimSpace(req.Cwd)
	req.ContextPrompt = strings.TrimSpace(req.ContextPrompt)

	if !validTerminalAIToken(req.Cluster) || !validTerminalAIToken(req.Instance) {
		return errors.New("缺少或无效的 cluster / instance")
	}
	if !validTerminalAIToken(req.SessionID) {
		return errors.New("需要有效的 session_id")
	}
	if req.Question == "" {
		return errors.New("提问内容不能为空")
	}
	if len([]rune(req.Question)) > terminalAIMaxQuestionRunes {
		return errors.New("提问内容过长")
	}
	if req.Action == "" {
		req.Action = "ask"
	}
	switch req.Action {
	case "ask", "explain", "fix", "followup":
	default:
		return errors.New("不支持的操作类型")
	}
	if len(req.Cwd) > 512 {
		req.Cwd = truncateUTF8Bytes(req.Cwd, 512)
	}
	if runes := []rune(req.ContextPrompt); len(runes) > terminalAIMaxContextRunes {
		req.ContextPrompt = string(runes[:terminalAIMaxContextRunes])
	}
	return nil
}

func validTerminalAIToken(value string) bool {
	if value == "" || len(value) > 128 || strings.Contains(value, "..") {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' || c == ':') {
			return false
		}
	}
	return true
}

func summarizeTerminalAIQuestion(action, question string) string {
	clean := strings.Join(strings.Fields(question), " ")
	if runes := []rune(clean); len(runes) > 120 {
		clean = string(runes[:120]) + "…"
	}
	return fmt.Sprintf("terminal_ai[%s]: %s", action, clean)
}

func resolveTerminalAIProviderEndpoint(rawPayload json.RawMessage) (terminalAIProviderEndpoint, error) {
	mp, err := decodeModelProviderPayload(rawPayload)
	if err != nil {
		return terminalAIProviderEndpoint{}, err
	}
	providerID, modelID, ok := strings.Cut(mp.Model, "/")
	if !ok {
		return terminalAIProviderEndpoint{}, ErrCredentialPayloadInvalid
	}
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	rawProvider, exists := mp.Provider[providerID]
	if !exists {
		return terminalAIProviderEndpoint{}, ErrCredentialPayloadInvalid
	}
	var entry struct {
		NPM     string `json:"npm"`
		API     string `json:"api"`
		Options struct {
			APIKey  string `json:"apiKey"`
			BaseURL string `json:"baseURL"`
			Kind    string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(rawProvider, &entry); err != nil {
		return terminalAIProviderEndpoint{}, ErrCredentialPayloadInvalid
	}
	apiKey := strings.TrimSpace(entry.Options.APIKey)
	if apiKey == "" {
		return terminalAIProviderEndpoint{}, ErrCredentialPayloadInvalid
	}

	kindHint := strings.ToLower(strings.TrimSpace(entry.Options.Kind))
	npmHint := strings.ToLower(strings.TrimSpace(entry.NPM))
	apiHint := strings.ToLower(strings.TrimSpace(entry.API))
	providerLower := strings.ToLower(providerID)

	kind := "openai"
	if kindHint == "anthropic" ||
		strings.Contains(npmHint, "anthropic") ||
		strings.Contains(apiHint, "anthropic") ||
		(kindHint == "" && providerLower == "anthropic") {
		kind = "anthropic"
	}

	endpointURL, err := buildTerminalAIUpstreamURL(kind, providerLower, strings.TrimSpace(entry.Options.BaseURL))
	if err != nil {
		return terminalAIProviderEndpoint{}, err
	}

	return terminalAIProviderEndpoint{
		ProviderID: providerID,
		ModelID:    modelID,
		FullModel:  providerID + "/" + modelID,
		Kind:       kind,
		URL:        endpointURL,
		APIKey:     apiKey,
	}, nil
}

var versionedPathSuffixRe = regexp.MustCompile(`/v\d+(?:alpha|beta)?$`)

func buildTerminalAIUpstreamURL(kind, providerLower, baseURL string) (string, error) {
	if baseURL == "" {
		if kind == "anthropic" {
			return "https://api.anthropic.com/v1/messages", nil
		}
		switch providerLower {
		case "deepseek":
			return "https://api.deepseek.com/v1/chat/completions", nil
		case "openai":
			return "https://api.openai.com/v1/chat/completions", nil
		default:
			return "", ErrCredentialPayloadInvalid
		}
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return "", ErrCredentialPayloadInvalid
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" {
		if !(terminalAIAllowLoopbackForTest && scheme == "http" && isLoopbackHost(parsed.Hostname())) {
			return "", ErrCredentialPayloadInvalid
		}
	}

	trimmed := strings.TrimRight(baseURL, "/")
	pathTrimmed := strings.TrimRight(parsed.Path, "/")
	if kind == "anthropic" {
		if strings.HasSuffix(pathTrimmed, "/messages") {
			return trimmed, nil
		}
		if versionedPathSuffixRe.MatchString(pathTrimmed) {
			return trimmed + "/messages", nil
		}
		return trimmed + "/v1/messages", nil
	}

	if strings.HasSuffix(pathTrimmed, "/chat/completions") {
		return trimmed, nil
	}
	if versionedPathSuffixRe.MatchString(pathTrimmed) {
		return trimmed + "/chat/completions", nil
	}
	return trimmed + "/v1/chat/completions", nil
}

func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func buildTerminalAIPrompts(req terminalAIRequest) (string, string) {
	systemPrompt := strings.Join([]string{
		"你是嵌入在 Linux/Kubernetes 运维交互终端中的单次 AI 命令助手。",
		"你没有工具调用权限，必须根据已知终端上下文直接给出准确、简洁的中文回答与可执行命令。",
		"必须只输出一个合法 JSON 对象，禁止输出任何 Markdown 外层解释或额外文字，格式严格为：",
		`{"explanation":"1到2句简洁中文说明","commands":[{"command":"可直接执行的单条shell命令","description":"简短中文用途说明"}]}`,
		"规则：",
		"1. commands 返回 0 到 3 条最直接、非全屏阻塞的命令（例如用 `top -bn1 | head -n 15` 代替交互式 `top`）。",
		"2. 每条 command 必须干净可执行，不要带 `$ ` 提示符前缀，不要臆造主机上不存在的路径。",
		"3. 若问题是解释命令或分析输出且不需要额外执行命令，commands 可为空数组 []。",
		"4. 涉及破坏性修改（如 rm/kill/重启/修改生产配置）时，在 description 中明确标注风险。",
	}, "\n")

	var userParts []string
	userParts = append(userParts, fmt.Sprintf("目标：%s/%s", req.Cluster, req.Instance))
	if req.Cwd != "" {
		userParts = append(userParts, "当前目录："+req.Cwd)
	}
	if req.Action != "" && req.Action != "ask" {
		userParts = append(userParts, "操作类型："+req.Action)
	}
	if req.ContextPrompt != "" {
		userParts = append(userParts, "终端上下文：\n"+req.ContextPrompt)
	}
	userParts = append(userParts, "用户请求：\n"+req.Question)
	return systemPrompt, strings.Join(userParts, "\n\n")
}

func invokeTerminalAIUpstream(
	ctx context.Context,
	ep terminalAIProviderEndpoint,
	req terminalAIRequest,
) (content string, usage terminalAIUsage, httpStatus int, errCode string, errMsg string) {
	systemPrompt, userPrompt := buildTerminalAIPrompts(req)

	var bodyBytes []byte
	var err error
	if ep.Kind == "anthropic" {
		bodyBytes, err = json.Marshal(map[string]any{
			"model":       ep.ModelID,
			"max_tokens":  900,
			"temperature": 0.1,
			"system":      systemPrompt,
			"messages": []map[string]string{
				{"role": "user", "content": userPrompt},
			},
		})
	} else {
		bodyBytes, err = json.Marshal(map[string]any{
			"model":       ep.ModelID,
			"max_tokens":  900,
			"temperature": 0.1,
			"messages": []map[string]string{
				{"role": "system", "content": systemPrompt},
				{"role": "user", "content": userPrompt},
			},
		})
	}
	if err != nil {
		return "", terminalAIUsage{}, http.StatusInternalServerError, "terminal_ai_encode_failed", "构造模型请求失败"
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", terminalAIUsage{}, http.StatusInternalServerError, "terminal_ai_request_invalid", "模型请求地址无效"
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if ep.Kind == "anthropic" {
		httpReq.Header.Set("x-api-key", ep.APIKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+ep.APIKey)
	}

	client := &http.Client{
		Transport: terminalAIHTTPTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", terminalAIUsage{}, http.StatusGatewayTimeout, "terminal_ai_timeout", "单次模型调用超时，请重试"
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return "", terminalAIUsage{}, 499, "terminal_ai_canceled", "已取消模型调用"
		}
		return "", terminalAIUsage{}, http.StatusBadGateway, "terminal_ai_upstream_failed", "连接模型服务失败，请稍后重试"
	}
	defer resp.Body.Close()

	// Drain up to cap and never echo raw upstream body to clients (prevents secret/internal leakage).
	respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, terminalAIMaxResponseBytes+1))
	if readErr != nil {
		return "", terminalAIUsage{}, http.StatusBadGateway, "terminal_ai_upstream_failed", "读取模型响应失败"
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return "", terminalAIUsage{}, http.StatusBadGateway, "terminal_ai_redirect_blocked", "模型服务返回了不允许的重定向"
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", terminalAIUsage{}, http.StatusBadGateway, "terminal_ai_upstream_auth_failed", "模型服务鉴权失败，请检查该目标绑定的 modelProvider 凭据"
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", terminalAIUsage{}, http.StatusTooManyRequests, "terminal_ai_rate_limited", "模型服务触发限流，请稍后重试"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", terminalAIUsage{}, http.StatusBadGateway, "terminal_ai_upstream_failed", fmt.Sprintf("模型服务返回异常状态（HTTP %d）", resp.StatusCode)
	}
	if len(respBytes) > terminalAIMaxResponseBytes {
		return "", terminalAIUsage{}, http.StatusBadGateway, "terminal_ai_response_too_large", "模型响应超出大小限制"
	}

	if ep.Kind == "anthropic" {
		var parsed struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(respBytes, &parsed); err != nil {
			return "", terminalAIUsage{}, http.StatusBadGateway, "terminal_ai_invalid_response", "模型返回格式无法解析"
		}
		var parts []string
		for _, block := range parsed.Content {
			if (block.Type == "" || block.Type == "text") && strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		u := terminalAIUsage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
			TotalTokens:  parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		}
		return strings.TrimSpace(strings.Join(parts, "\n")), u, http.StatusOK, "", ""
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil || len(parsed.Choices) == 0 {
		return "", terminalAIUsage{}, http.StatusBadGateway, "terminal_ai_invalid_response", "模型返回格式无法解析"
	}
	text := extractOpenAIMessageText(parsed.Choices[0].Message.Content)
	u := terminalAIUsage{
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
		TotalTokens:  parsed.Usage.TotalTokens,
	}
	if u.TotalTokens == 0 && (u.InputTokens > 0 || u.OutputTokens > 0) {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return text, u, http.StatusOK, "", ""
}

func extractOpenAIMessageText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(trimmed, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(trimmed, &parts); err == nil {
		var out []string
		for _, p := range parts {
			if strings.TrimSpace(p.Text) != "" {
				out = append(out, p.Text)
			}
		}
		return strings.TrimSpace(strings.Join(out, "\n"))
	}
	return ""
}

var (
	terminalAIJSONFenceRe  = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
	terminalAIShellFenceRe = regexp.MustCompile("(?s)```(?:bash|sh|shell|zsh)?\\s*\n(.*?)```")
)

func parseTerminalAIOutput(raw string) (string, []terminalAICommand) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	// 1. Try direct JSON or fenced JSON object.
	candidates := []string{trimmed}
	if m := terminalAIJSONFenceRe.FindStringSubmatch(trimmed); len(m) == 2 {
		candidates = append([]string{strings.TrimSpace(m[1])}, candidates...)
	}
	if start := strings.IndexByte(trimmed, '{'); start >= 0 {
		if end := strings.LastIndexByte(trimmed, '}'); end > start {
			candidates = append(candidates, trimmed[start:end+1])
		}
	}

	for _, candidate := range candidates {
		var obj struct {
			Explanation string              `json:"explanation"`
			Summary     string              `json:"summary"`
			Commands    []terminalAICommand `json:"commands"`
		}
		if err := json.Unmarshal([]byte(candidate), &obj); err == nil {
			exp := strings.TrimSpace(obj.Explanation)
			if exp == "" {
				exp = strings.TrimSpace(obj.Summary)
			}
			cmds := normalizeTerminalAICommands(obj.Commands)
			if exp != "" || len(cmds) > 0 {
				return clipTerminalAIExplanation(exp), cmds
			}
		}
	}

	// 2. Fallback: extract fenced shell blocks + surrounding prose.
	matches := terminalAIShellFenceRe.FindAllStringSubmatch(trimmed, -1)
	var cmds []terminalAICommand
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		block := strings.TrimSpace(m[1])
		if block == "" {
			continue
		}
		cmds = append(cmds, terminalAICommand{Command: block})
	}
	prose := strings.TrimSpace(terminalAIShellFenceRe.ReplaceAllString(trimmed, ""))
	return clipTerminalAIExplanation(prose), normalizeTerminalAICommands(cmds)
}

func normalizeTerminalAICommands(raw []terminalAICommand) []terminalAICommand {
	out := make([]terminalAICommand, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		cmd := strings.TrimSpace(item.Command)
		cmd = strings.TrimPrefix(cmd, "$ ")
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		if _, dup := seen[cmd]; dup {
			continue
		}
		seen[cmd] = struct{}{}
		desc := strings.TrimSpace(item.Description)
		if runes := []rune(desc); len(runes) > 160 {
			desc = string(runes[:160]) + "…"
		}
		out = append(out, terminalAICommand{
			Command:     cmd,
			Description: desc,
		})
		if len(out) >= terminalAIMaxCommands {
			break
		}
	}
	return out
}

func clipTerminalAIExplanation(text string) string {
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > terminalAIMaxExplanationLen {
		return string(runes[:terminalAIMaxExplanationLen]) + "…"
	}
	return text
}
