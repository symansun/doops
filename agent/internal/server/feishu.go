package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/symansun/doops/agent/api"
)

// FeishuConfig 是飞书机器人接入配置（全部来自 gateway 环境变量）。
// 配置 VerificationToken 即可校验事件来源；再配 EncryptKey 时同时校验签名并解密事件体。
type FeishuConfig struct {
	AppID             string
	AppSecret         string
	VerificationToken string
	EncryptKey        string
	// Users 把飞书 open_id 映射到 gateway 用户名，权限完全沿用 gateway 授权与审批策略
	Users map[string]string
	// DefaultCluster 用于 "exec <instance> <cmd>" 这类省略集群的写法
	DefaultCluster string
	// ReplyWebhook 是未配置应用凭证时的兜底回复通道（飞书自定义机器人 webhook）
	ReplyWebhook string
	// APIBase 便于测试与私有化部署，默认 https://open.feishu.cn
	APIBase string
}

const feishuMaxReplyRunes = 1500

var feishuMentionPattern = regexp.MustCompile(`@_user_\d+`)

// HasFeishu 表示 gateway 是否启用了飞书事件入口。
func (h *GatewayHub) HasFeishu() bool {
	return h.opts.Feishu != nil
}

// HandleFeishuEvent 接收飞书事件订阅回调：先校验来源，再异步执行命令并回复。
// 必须在 3 秒内响应，因此实际执行放到 goroutine。
func (h *GatewayHub) HandleFeishuEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := h.opts.Feishu
	if cfg == nil {
		http.Error(w, "feishu integration not configured", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	if cfg.EncryptKey != "" {
		if !verifyFeishuSignature(r.Header, body, cfg.EncryptKey) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}
	payload, err := decryptFeishuPayload(body, cfg.EncryptKey)
	if err != nil {
		http.Error(w, "cannot decrypt payload", http.StatusBadRequest)
		return
	}

	var event feishuEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	// URL 验证：回显 challenge
	if event.Type == "url_verification" {
		if !feishuTokenOK(cfg, event.Token) {
			http.Error(w, "invalid verification token", http.StatusUnauthorized)
			return
		}
		writeJSONHTTP(w, map[string]string{"challenge": event.Challenge})
		return
	}
	if !feishuTokenOK(cfg, event.Header.Token) {
		http.Error(w, "invalid verification token", http.StatusUnauthorized)
		return
	}
	// 先回 200，执行与回复走异步，避免飞书回调超时重试
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))

	if event.Header.EventType != "im.message.receive_v1" {
		return
	}
	if event.Event.Sender.SenderType != "user" || event.Event.Message.MessageType != "text" {
		return
	}
	if h.feishuDuplicate(event.Header.EventID) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		h.handleFeishuMessage(ctx, cfg, event)
	}()
}

// handleFeishuMessage 同步处理单条文本消息（测试直接调用）。
func (h *GatewayHub) handleFeishuMessage(ctx context.Context, cfg *FeishuConfig, event feishuEvent) {
	openID := strings.TrimSpace(event.Event.Sender.SenderID.OpenID)
	username, ok := cfg.Users[openID]
	if !ok {
		h.replyFeishu(ctx, cfg, openID, "未绑定 doops 账号：请管理员在 gateway 配置 DOOPS_FEISHU_USER_MAP 中将 open_id 映射到用户名。")
		return
	}
	user, err := h.store.FindUserByName(username)
	if err != nil {
		h.replyFeishu(ctx, cfg, openID, fmt.Sprintf("doops 用户 %q 不存在或已停用。", username))
		return
	}

	text, err := parseFeishuTextContent(event.Event.Message.Content)
	if err != nil {
		h.replyFeishu(ctx, cfg, openID, "无法解析消息内容，请使用 exec/ask 命令。")
		return
	}
	auth := TokenAuth{UserID: user.ID, Kind: TokenKindUser}
	reply := h.runFeishuCommand(ctx, cfg, auth, text)
	h.replyFeishu(ctx, cfg, openID, reply)
}

// runFeishuCommand 执行解析后的命令并返回回复文本。
func (h *GatewayHub) runFeishuCommand(ctx context.Context, cfg *FeishuConfig, auth TokenAuth, text string) string {
	cmd, err := parseFeishuCommand(text, cfg.DefaultCluster)
	if err != nil {
		return err.Error()
	}
	switch cmd.Action {
	case "help":
		return feishuHelpText()
	case "targets":
		return h.feishuTargets(auth)
	case "exec":
		out, errMsg := h.callFeishuTool(ctx, auth, cmd.Cluster, cmd.Instance, "doops_shell", map[string]interface{}{
			"command": cmd.Argument, "session": "feishu",
		})
		if errMsg != "" {
			return "执行失败：" + errMsg
		}
		return truncateRunes(out, feishuMaxReplyRunes)
	case "ask":
		out, errMsg := h.callFeishuTool(ctx, auth, cmd.Cluster, cmd.Instance, "doops_agent_prompt", map[string]interface{}{
			"instruction": cmd.Argument, "session": "feishu",
		})
		if errMsg != "" {
			return "AI 执行失败：" + errMsg
		}
		return truncateRunes(out, feishuMaxReplyRunes)
	}
	return feishuHelpText()
}

func (h *GatewayHub) feishuTargets(auth TokenAuth) string {
	targets := h.ListTargets()
	var b strings.Builder
	b.WriteString("可用节点（已按你的授权过滤）：\n")
	shown := 0
	for _, target := range targets {
		if !h.store.UserCan(auth.UserID, target.Cluster, target.Instance, ActionTargetsList) {
			continue
		}
		status := target.Status
		if status == "" {
			status = "online"
		}
		b.WriteString(fmt.Sprintf("- %s/%s [%s]\n", target.Cluster, target.Instance, status))
		shown++
	}
	if shown == 0 {
		return "没有你可访问的在线节点。"
	}
	return b.String()
}

// callFeishuTool 复用 gateway 的 tools/call 路径（鉴权、审批闸、并发控制、审计全部生效）。
func (h *GatewayHub) callFeishuTool(ctx context.Context, auth TokenAuth, cluster, instance, tool string, arguments map[string]interface{}) (string, string) {
	args, err := json.Marshal(arguments)
	if err != nil {
		return "", err.Error()
	}
	req := api.JSONRPCRequest{JSONRPC: "2.0", ID: "feishu", Method: "tools/call"}
	req.Params, _ = json.Marshal(api.ToolCallParams{Name: tool, Arguments: args})

	var out strings.Builder
	var rpcErr string
	collect := func(raw []byte) error {
		var msg struct {
			Result *struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
			Error interface{} `json:"error"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil
		}
		if msg.Error != nil {
			rpcErr = fmt.Sprintf("%v", msg.Error)
			return nil
		}
		if msg.Result != nil {
			for _, part := range msg.Result.Content {
				if part.Type == "text" && part.Text != "" {
					out.WriteString(part.Text)
				}
			}
			if msg.Result.IsError {
				rpcErr = strings.TrimSpace(out.String())
			}
		}
		return nil
	}
	writeJSON := func(v interface{}) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return collect(raw)
	}
	_ = h.handleGatewayToolCall(auth, cluster, instance, req, collect, writeJSON)
	if rpcErr != "" {
		return "", rpcErr
	}
	return out.String(), ""
}

func (h *GatewayHub) replyFeishu(ctx context.Context, cfg *FeishuConfig, openID, text string) {
	if strings.TrimSpace(openID) == "" || strings.TrimSpace(text) == "" {
		return
	}
	if cfg.AppID != "" && cfg.AppSecret != "" {
		if err := feishuSendText(ctx, cfg, openID, text); err != nil {
			log.Printf("feishu reply failed: %v", err)
		}
		return
	}
	if cfg.ReplyWebhook != "" {
		if err := postAlert(ctx, cfg.ReplyWebhook, text); err != nil {
			log.Printf("feishu webhook reply failed: %v", err)
		}
		return
	}
	log.Printf("feishu reply skipped (no app credentials or reply webhook): %s", truncateRunes(text, 200))
}

// ---------------------------------------------------------------------------
// 事件解析与校验
// ---------------------------------------------------------------------------

type feishuEvent struct {
	Schema    string `json:"schema"`
	Type      string `json:"type"`
	Token     string `json:"token"`
	Challenge string `json:"challenge"`
	Encrypt   string `json:"encrypt"`
	Header    struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
		Token     string `json:"token"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
				UserID string `json:"user_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"message"`
	} `json:"event"`
}

func feishuTokenOK(cfg *FeishuConfig, token string) bool {
	if cfg.VerificationToken == "" {
		return true
	}
	return token == cfg.VerificationToken
}

// verifyFeishuSignature 校验 X-Lark-Signature：sha256(timestamp + nonce + encryptKey + body)。
func verifyFeishuSignature(header http.Header, body []byte, encryptKey string) bool {
	timestamp := strings.TrimSpace(header.Get("X-Lark-Request-Timestamp"))
	nonce := strings.TrimSpace(header.Get("X-Lark-Request-Nonce"))
	got := strings.TrimSpace(header.Get("X-Lark-Signature"))
	if timestamp == "" || nonce == "" || got == "" {
		return false
	}
	seed := timestamp + nonce + encryptKey + string(body)
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:]) == got
}

// decryptFeishuPayload 处理加密事件体（AES-256-CBC，key=sha256(encryptKey)，iv 为密文前 16 字节）。
func decryptFeishuPayload(body []byte, encryptKey string) ([]byte, error) {
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Encrypt == "" {
		return body, nil
	}
	if encryptKey == "" {
		return nil, fmt.Errorf("encrypted event but no encrypt key configured")
	}
	raw, err := base64.StdEncoding.DecodeString(envelope.Encrypt)
	if err != nil || len(raw) < aes.BlockSize {
		return nil, fmt.Errorf("invalid encrypted payload")
	}
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(raw)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, raw[:aes.BlockSize]).CryptBlocks(plain, raw[aes.BlockSize:])
	return unpadPKCS7(plain, aes.BlockSize)
}

// unpadPKCS7 去掉 CBC 分组填充（飞书事件加密用 PKCS7）。
func unpadPKCS7(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padded payload")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	return data[:len(data)-pad], nil
}

func parseFeishuTextContent(content string) (string, error) {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return "", err
	}
	text := feishuMentionPattern.ReplaceAllString(payload.Text, " ")
	return strings.TrimSpace(text), nil
}

// ---------------------------------------------------------------------------
// 命令解析
// ---------------------------------------------------------------------------

type feishuCommand struct {
	Action   string
	Cluster  string
	Instance string
	Argument string
}

// parseFeishuCommand 只接受显式命令，避免自然语言被误当 shell 执行：
//
//	help | targets | exec <cluster>/<instance> <cmd> | ask <cluster>/<instance> <question>
func parseFeishuCommand(text, defaultCluster string) (feishuCommand, error) {
	text = feishuMentionPattern.ReplaceAllString(text, " ")
	cleaned := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "/"))
	if cleaned == "" {
		return feishuCommand{}, fmt.Errorf("空消息。%s", feishuHelpHint())
	}
	fields := strings.Fields(cleaned)
	action := strings.ToLower(fields[0])
	switch action {
	case "help", "帮助", "?":
		return feishuCommand{Action: "help"}, nil
	case "targets", "节点":
		return feishuCommand{Action: "targets"}, nil
	case "exec", "ask":
		if len(fields) < 3 {
			return feishuCommand{}, fmt.Errorf("用法：%s <cluster>/<instance> <%s>", action, map[string]string{"exec": "命令", "ask": "问题"}[action])
		}
		cluster, instance, err := splitFeishuTarget(fields[1], defaultCluster)
		if err != nil {
			return feishuCommand{}, err
		}
		return feishuCommand{Action: action, Cluster: cluster, Instance: instance, Argument: strings.Join(fields[2:], " ")}, nil
	}
	return feishuCommand{}, fmt.Errorf("未识别命令 %q。%s", fields[0], feishuHelpHint())
}

func splitFeishuTarget(raw, defaultCluster string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("目标不能为空，格式：<cluster>/<instance>")
	}
	if !strings.Contains(raw, "/") {
		if strings.TrimSpace(defaultCluster) == "" {
			return "", "", fmt.Errorf("目标需写成 <cluster>/<instance>（未配置默认集群）")
		}
		return strings.TrimSpace(defaultCluster), raw, nil
	}
	parts := strings.SplitN(raw, "/", 2)
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("目标格式错误，应为 <cluster>/<instance>")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func feishuHelpHint() string { return "发送 help 查看可用命令。" }

func feishuHelpText() string {
	return strings.Join([]string{
		"DoOps 飞书机器人命令：",
		"- help：查看帮助",
		"- targets：列出你有权限的节点",
		"- exec <cluster>/<instance> <命令>：在节点执行 shell 命令",
		"- ask <cluster>/<instance> <问题>：让节点上的 doagent 处理任务",
		"",
		"生产审批目标会先返回审批单 ID，审批人执行 doops admin approve 后，",
		"用 doops CLI 加 --approval 重放同一命令。",
	}, "\n")
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "\n...(已截断)"
}

// ---------------------------------------------------------------------------
// 回复通道
// ---------------------------------------------------------------------------

var (
	feishuTokenMu    sync.Mutex
	feishuTokenCache = map[string]feishuCachedToken{}
)

type feishuCachedToken struct {
	Token     string
	ExpiresAt time.Time
}

func feishuBaseURL(cfg *FeishuConfig) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/")
	if base == "" {
		base = "https://open.feishu.cn"
	}
	return base
}

func feishuTenantToken(ctx context.Context, cfg *FeishuConfig) (string, error) {
	key := cfg.AppID + "|" + feishuBaseURL(cfg)
	feishuTokenMu.Lock()
	if cached, ok := feishuTokenCache[key]; ok && time.Now().Before(cached.ExpiresAt) {
		feishuTokenMu.Unlock()
		return cached.Token, nil
	}
	feishuTokenMu.Unlock()

	body, _ := json.Marshal(map[string]string{"app_id": cfg.AppID, "app_secret": cfg.AppSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuBaseURL(cfg)+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("decode tenant token: %w", err)
	}
	if result.Code != 0 || result.TenantAccessToken == "" {
		return "", fmt.Errorf("tenant token error: code=%d msg=%s", result.Code, result.Msg)
	}
	expire := time.Duration(result.Expire) * time.Second
	if expire <= 0 {
		expire = 2 * time.Hour
	}
	feishuTokenMu.Lock()
	feishuTokenCache[key] = feishuCachedToken{Token: result.TenantAccessToken, ExpiresAt: time.Now().Add(expire - 2*time.Minute)}
	feishuTokenMu.Unlock()
	return result.TenantAccessToken, nil
}

// feishuSendText 通过应用 API 向 open_id 发送文本消息。
func feishuSendText(ctx context.Context, cfg *FeishuConfig, openID, text string) error {
	token, err := feishuTenantToken(ctx, cfg)
	if err != nil {
		return err
	}
	content, _ := json.Marshal(map[string]string{"text": text})
	body, _ := json.Marshal(map[string]string{
		"receive_id": openID,
		"msg_type":   "text",
		"content":    string(content),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuBaseURL(cfg)+"/open-apis/im/v1/messages?receive_id_type=open_id", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(raw, &result)
	if resp.StatusCode >= 300 || result.Code != 0 {
		return fmt.Errorf("send message failed: HTTP %s code=%d msg=%s", resp.Status, result.Code, result.Msg)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 事件去重（飞书在超时/异常时会重推同一事件）
// ---------------------------------------------------------------------------

func (h *GatewayHub) feishuDuplicate(eventID string) bool {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return false
	}
	h.feishuMu.Lock()
	defer h.feishuMu.Unlock()
	now := time.Now()
	for id, seenAt := range h.feishuSeen {
		if now.Sub(seenAt) > 5*time.Minute {
			delete(h.feishuSeen, id)
		}
	}
	if _, ok := h.feishuSeen[eventID]; ok {
		return true
	}
	h.feishuSeen[eventID] = now
	return false
}
