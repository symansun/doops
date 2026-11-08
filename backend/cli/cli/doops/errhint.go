package main

import (
	"fmt"
	"os"
	"strings"
)

// fatalErr keeps the original failure and adds a next step when its category is known.
func fatalErr(err error) {
	fmt.Printf("Error: %v\n", err)
	if hint := errorHint(err); hint != "" {
		fmt.Printf("Hint: %s\n", hint)
	}
	os.Exit(1)
}

func errorHint(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	// These domains have their own authority and recovery instructions. A 401 in
	// their message must not be turned into a Gateway user-token diagnosis.
	if containsAny(msg, "credential_", "edge_enrollment_auth_failed") {
		return ""
	}
	switch {
	case containsAny(msg, "gateway_auth_failed", "no login for gateway"):
		return "Gateway 登录态缺失、已过期或被撤销。重新登录：doops login --target <target>；同一 Gateway 的 target 共享登录态。"
	case containsAny(msg, "http 401", "http_401", "401 unauthorized", "unauthorized"):
		return "认证请求被拒绝，先核对响应来源；确认是 Gateway 登录失败后，运行 doops login --target <target>。"
	case containsAny(msg, "http 403", "http_403", "403 forbidden", "forbidden"):
		return "请求被权限检查拒绝。用 doops targets --target <target> 核对可见目标；若为 Gateway 目标授权问题，请管理员检查当前用户对 cluster/instance 的授权。"
	case containsAny(msg, "blocked_command(", "blocked by agent guardrail"):
		return "命令被 Agent 护栏拦截，规则 ID 见错误详情。先核对命令和路径；确认为误伤时，由管理员审查 DOOPS_BLOCK_DISABLE 配置，不绕过拦截。"
	case strings.Contains(msg, "agent") && strings.Contains(msg, "offline"), containsAny(msg, "agent_not_registered", "not registered", "no online agent", "no agent", "unknown cluster", "unknown instance"):
		return "目标 Agent 离线或未注册。先用 doops targets --target <target> 核查；未注册时按接入指南配置 doops-agent -gateway-url <url> -cluster <cluster> -instance <instance>。"
	case containsAny(msg, "connection lost", "failed to connect", "connection refused", "no such host", "network is unreachable") && !strings.Contains(msg, "execution_outcome_unknown"):
		return "无法连接 Gateway/Agent。依次检查：1) doops list 核对 target 路由；2) gateway 地址与网络可达性；3) doops targets --target <target> 核查 Agent 在线状态。已发送的变更先核查结果，不自动重试。"
	case containsAny(msg, "execution_outcome_unknown", "timed out", "timeout", "deadline exceeded"):
		return "尚未确认远端执行终态，先用同一 target 和 session 只读核查实际状态，不重放变更。长任务可用 doops exec --bg；DOOPS_CLI_CALL_TIMEOUT 可设置调用等待时长。"
	}
	return ""
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
