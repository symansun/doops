// 文件页「清理工作区」的参数拼装、本地校验和结果文案。
// 参数与 Agent 的 doops_clean_workspace 一致（backend/agent/internal/server/workspace_upload.go）。
// 「检查部署」只在 CLI（doops check）里保留，控制台不再提供该动作。

export interface MaintenanceLog {
  lines: string[]
  errors: string[]
}

export const EMPTY_MAINTENANCE_LOG: MaintenanceLog = { lines: [], errors: [] }

// 与 workspace-path.ts 和 /api/rpc 的会话 ID 规则一致。
const SESSION_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/

// session_id 用于审计和 RPC 归属，workspace 是 Agent 实际删除的工作区名。
export function cleanWorkspaceRequest(sessionId: string): Record<string, string> {
  if (!SESSION_ID.test(sessionId) || sessionId.includes("..")) throw new Error("当前会话无效，无法清理工作区")
  return { session_id: sessionId, workspace: sessionId }
}

type Rewrite = [RegExp, (m: RegExpExecArray) => string]

const CLEAN_ERRORS: Rewrite[] = [
  [/^unsafe session id: ([\s\S]*)$/, (m) => `工作区会话无效：${m[1]}`],
  [/^invalid clean workspace params: ([\s\S]*)$/, (m) => `清理参数格式无效：${m[1]}`],
]

const CLEAN_RESULTS: Rewrite[] = [
  [/^Cleaned workspace and bare repo: ([\s\S]+)$/, (m) => `已删除工作区和 Git 裸仓库：${m[1]}`],
]

function rewrite(rules: Rewrite[], raw: string): string {
  const text = raw.trim()
  for (const [pattern, render] of rules) {
    const match = pattern.exec(text)
    if (match) return render(match)
  }
  return text
}

// Agent 的英文报错改写成中文；不认识的报错（Gateway 授权、连接失败等）原样展示。
export function cleanErrorText(raw: string): string {
  return rewrite(CLEAN_ERRORS, raw)
}

export function cleanResultText(raw: string): string {
  return rewrite(CLEAN_RESULTS, raw)
}

export function maintenanceOutput(log: MaintenanceLog, text: string): MaintenanceLog {
  return text ? { ...log, lines: [...log.lines, text] } : log
}

// callTool 先推 error 事件再抛出同一条错误，同一条错误只记一次。
export function maintenanceFailure(log: MaintenanceLog, raw: string): MaintenanceLog {
  const text = cleanErrorText(raw) || "执行失败"
  return log.errors.includes(text) ? log : { ...log, errors: [...log.errors, text] }
}
