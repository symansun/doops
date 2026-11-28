import assert from "node:assert/strict"
import test from "node:test"
import { buildTerminalHandbackPrompt, buildTerminalInlineContext } from "./terminal-ai-context.ts"
import { MAX_TERMINAL_AI_CHARS } from "./terminal-ai.ts"

test("buildTerminalInlineContext attaches recent blocks, prioritizes focus block, preserves raw text, and bounds size", () => {
  const blocks = [
    { id: "b1", command: "ls -la", output: "app.js\nREADME.md", exitCode: 0, durationMs: 25 },
    { id: "b2", command: "cat config.env", output: "DB_HOST=10.0.0.12\nDB_PORT=3306", exitCode: 0, durationMs: 12 },
    { id: "b3", command: "kubectl get pods", output: "nginx-1 CrashLoopBackOff", exitCode: 0, durationMs: 420 },
    { id: "b4", command: "kubectl logs nginx-1", output: "error: bind 0.0.0.0:80 failed", exitCode: 1, durationMs: 310 },
  ]

  // 常规提问：默认附带最近 3 条（b4, b3, b2），原文透传不做改写
  const regular = buildTerminalInlineContext({
    target: { cluster: "prod", instance: "node-1" },
    cwd: "/srv/app",
    terminalMode: "pty",
    question: "为什么 nginx 起不来？",
    blocks,
  })
  assert.deepEqual(regular.includedBlockIds, ["b4", "b3", "b2"])
  assert.equal(regular.prompt.includes("DB_HOST=10.0.0.12"), true)
  assert.equal(regular.prompt.includes("[已屏蔽"), false)
  assert.ok(regular.summaryLabel.includes("附带 3 条命令"))
  assert.equal(regular.summaryLabel.includes("已屏蔽"), false)

  // 聚焦修复：聚焦 b4 + 前 1 条历史（b3）
  const focused = buildTerminalInlineContext({
    target: { cluster: "prod", instance: "node-1" },
    cwd: "/srv/app",
    terminalMode: "pty",
    question: "请帮我修复",
    blocks,
    focusBlock: blocks[3],
    focusAction: "fix",
  })
  assert.deepEqual(focused.includedBlockIds, ["b4", "b3"])
  assert.ok(focused.prompt.includes("[聚焦的命令块"))

  // 超大输出严格不超过 MAX_TERMINAL_AI_CHARS
  const huge = buildTerminalInlineContext({
    target: { cluster: "prod", instance: "node-1" },
    question: "分析日志",
    blocks: [{ id: "big", command: "cat huge.log", output: "X".repeat(120_000), exitCode: 0 }],
  })
  assert.ok(huge.charCount <= MAX_TERMINAL_AI_CHARS)

  // 人机协作回传：从右侧交互终端把最近命令结果发回左侧 AI 对话
  const handback = buildTerminalHandbackPrompt({
    target: { cluster: "prod", instance: "node-1" },
    cwd: "/srv/app",
    blocks,
  })
  assert.deepEqual(handback.includedBlockIds, ["b4", "b3", "b2"])
  assert.equal(handback.prompt.includes("DB_HOST=10.0.0.12"), true)
  assert.match(handback.prompt, /右侧交互终端/)
})
