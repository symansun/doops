package main

import (
	"fmt"
	"os"
	"strings"
)

// fatalErr 打印错误并退出。能识别类别时附上一行可操作的下一步建议（Hint），
// 把 "发生了什么 → 你下一步做什么" 固定配对，避免用户在文档里翻故障排查。
func fatalErr(err error) {
	fmt.Printf("Error: %v\n", err)
	if hint := errorHint(err); hint != "" {
		fmt.Printf("Hint: %s\n", hint)
	}
	os.Exit(1)
}

// fatalServerNotFound 处理 target 配置不存在的统一出口。
func fatalServerNotFound(target string) {
	fmt.Printf("Error: Server '%s' not found.\n", target)
	fmt.Printf("Hint: 该 target 尚未配置。先用 doops targets 查看已有 target；新增：doops login --target %s --gateway <url> --username <name>\n", target)
	os.Exit(1)
}

// errorHint 按错误文本分类返回可操作提示；未识别返回空串。
// 注意匹配顺序：HTTP 401/403 与连接错误常同时出现（"failed to connect ... (HTTP 401: ...)"），
// 鉴权类必须排在连接类之前。
func errorHint(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case containsAny(msg, "http 401", "401 unauthorized", "unauthorized"):
		return "token 已过期或无效。重新登录：doops login --target <target> --gateway <url> --username <name>"

	case containsAny(msg, "http 403", "403 forbidden", "forbidden", "missing scope", "insufficient scope", "scope"):
		return "权限不足：当前 token 缺少该 target/action 的授权。请用 doops targets 确认可见范围，或联系 gateway 管理员追加授权。"

	case containsAny(msg, "approval required"):
		return "该目标属于审批管控范围（如生产环境）。审批单已创建（ID 见错误详情）：请审批人执行 doops admin approve --id <apr_xxx> 后，重发同一命令并加 --approval <apr_xxx>。"

	case containsAny(msg, "approval"):
		return "审批单校验失败：确认 1) 审批单已被 approve 且未过期（24h）；2) 重发命令与申请时完全一致（同 target、同参数）；3) 审批单未被消费过（一次性）。查看：doops admin approvals list。"

	case containsAny(msg, "blocked by agent guardrail"):
		return "命令被 agent 端命令护栏拦截（[规则ID] 见错误详情）。确属误伤时，由管理员在 agent 侧设置 DOOPS_BLOCK_DISABLE=<规则ID> 豁免；或改写命令绕开高危模式。"

	case containsAny(msg, "offline", "not registered", "no agent", "unknown cluster", "unknown instance", "not found"):
		return "目标 agent 不在线或未注册。先用 doops targets 核查在线状态；未注册则在目标机执行：doops-agent -gateway-url <url> -cluster <cluster> -instance <name>"

	case containsAny(msg, "connection lost", "failed to connect", "connection refused", "no such host", "network is unreachable"):
		return "无法连接 gateway/agent。依次检查：1) doops targets 确认 target 配置；2) gateway 地址与网络可达性；3) agent 在线状态。"

	case containsAny(msg, "timed out", "timeout", "deadline exceeded"):
		return "调用超时，命令可能仍在远端执行。长任务建议 doops bg 异步执行后用 doops task 查看进度；或调大超时：export DOOPS_CLI_CALL_TIMEOUT=60m"
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
