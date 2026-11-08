package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// gatewayMetrics 收集 gateway 运行期指标，供 /metrics 端点以 Prometheus 文本格式导出。
// 不引入外部依赖，计数器用 atomic + sync.Map 手写。
type gatewayMetrics struct {
	toolCalls        sync.Map // key: action+"\x00"+status -> *int64
	guardrailBlocked int64
	wsClients        int64
}

func (m *gatewayMetrics) recordToolCall(action, status string) {
	if action == "" {
		action = "unknown"
	}
	if status == "" {
		status = "unknown"
	}
	key := action + "\x00" + status
	if v, ok := m.toolCalls.Load(key); ok {
		atomic.AddInt64(v.(*int64), 1)
		return
	}
	v, _ := m.toolCalls.LoadOrStore(key, new(int64))
	atomic.AddInt64(v.(*int64), 1)
}

func (m *gatewayMetrics) snapshotToolCalls() [][2]string {
	keys := make([]string, 0)
	m.toolCalls.Range(func(k, _ interface{}) bool {
		keys = append(keys, k.(string))
		return true
	})
	sort.Strings(keys)
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		v, _ := m.toolCalls.Load(k)
		parts := strings.SplitN(k, "\x00", 2)
		out = append(out, [2]string{parts[0] + "\x00" + parts[1], fmt.Sprintf("%d", atomic.LoadInt64(v.(*int64)))})
	}
	return out
}

// metricsAccessAllowed 实现 /metrics 访问控制：
// 回环地址直接放行；非回环地址要求 Authorization: Bearer $DOOPS_METRICS_TOKEN；
// 未配置 DOOPS_METRICS_TOKEN 时非回环地址一律拒绝。
func metricsAccessAllowed(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	token := strings.TrimSpace(os.Getenv("DOOPS_METRICS_TOKEN"))
	if token == "" {
		return false
	}
	return bearerToken(r) == token
}

// HandleMetrics 以 Prometheus 文本格式导出 gateway 指标。
func (h *GatewayHub) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if !metricsAccessAllowed(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	h.mu.RLock()
	agentsOnline := len(h.agents)
	h.mu.RUnlock()

	h.opsMu.Lock()
	opsActive := h.active
	h.opsMu.Unlock()

	m := &h.metrics
	var b strings.Builder
	fmt.Fprintf(&b, "# HELP doops_gateway_agents_online Currently connected agents.\n")
	fmt.Fprintf(&b, "# TYPE doops_gateway_agents_online gauge\n")
	fmt.Fprintf(&b, "doops_gateway_agents_online %d\n", agentsOnline)

	fmt.Fprintf(&b, "# HELP doops_gateway_ws_clients Currently connected client RPC websocket connections.\n")
	fmt.Fprintf(&b, "# TYPE doops_gateway_ws_clients gauge\n")
	fmt.Fprintf(&b, "doops_gateway_ws_clients %d\n", atomic.LoadInt64(&m.wsClients))

	fmt.Fprintf(&b, "# HELP doops_gateway_operations_active Currently running forwarded operations.\n")
	fmt.Fprintf(&b, "# TYPE doops_gateway_operations_active gauge\n")
	fmt.Fprintf(&b, "doops_gateway_operations_active %d\n", opsActive)

	fmt.Fprintf(&b, "# HELP doops_gateway_tool_calls_total Tool calls forwarded by the gateway, by action and final audit status.\n")
	fmt.Fprintf(&b, "# TYPE doops_gateway_tool_calls_total counter\n")
	for _, kv := range m.snapshotToolCalls() {
		parts := strings.SplitN(kv[0], "\x00", 2)
		fmt.Fprintf(&b, "doops_gateway_tool_calls_total{action=%q,status=%q} %s\n", parts[0], parts[1], kv[1])
	}

	fmt.Fprintf(&b, "# HELP doops_gateway_guardrail_blocked_total Exec calls blocked by the agent-side command guardrail.\n")
	fmt.Fprintf(&b, "# TYPE doops_gateway_guardrail_blocked_total counter\n")
	fmt.Fprintf(&b, "doops_gateway_guardrail_blocked_total %d\n", atomic.LoadInt64(&m.guardrailBlocked))

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}
