import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import {
  MAX_TERMINAL_AI_CHARS,
  buildTerminalAiPrompt,
  buildTerminalComposerPrefill,
  clampContextMenuPosition,
  clipTerminalTextForAi,
  dispatchTerminalAiRequest,
  fencedTerminalBlock,
  hasTerminalSelection,
  normalizeTerminalCopyText,
  summarizeTerminalSelection,
} from "./terminal-ai.ts"

test("summarizeTerminalSelection distinguishes empty, ANSI-only and multi-line selections", () => {
  assert.equal(hasTerminalSelection(""), false)
  assert.equal(hasTerminalSelection("   \n\t  "), false)
  assert.equal(hasTerminalSelection("\u001b[31m   \u001b[0m"), false)
  assert.equal(summarizeTerminalSelection("").hasSelection, false)
  assert.match(summarizeTerminalSelection("").label, /未选中文本/)

  const single = summarizeTerminalSelection("Error: pod api-0 CrashLoopBackOff")
  assert.deepEqual(single, {
    hasSelection: true,
    lines: 1,
    chars: 33,
    label: "已选中 1 行 · 33 字符",
  })

  const multiWithTrailingNewline = summarizeTerminalSelection("\u001b[31mline1\u001b[0m\r\nline2\r\nline3\n")
  assert.equal(multiWithTrailingNewline.hasSelection, true)
  assert.equal(multiWithTrailingNewline.lines, 3)
  assert.equal(multiWithTrailingNewline.chars, "line1\nline2\nline3\n".length)
})

test("normalizeTerminalCopyText preserves indentation while removing NULs and CRLF", () => {
  assert.equal(normalizeTerminalCopyText("  foo\u0000bar\r\n  baz  "), "  foobar\n  baz  ")
})

test("fencedTerminalBlock escalates fence length when selection contains backticks", () => {
  assert.equal(fencedTerminalBlock("kubectl get pods"), "```console\nkubectl get pods\n```")
  const nested = fencedTerminalBlock("cat <<'EOF'\n```yaml\nkind: Pod\n```\nEOF")
  assert.match(nested, /^````console\n/)
  assert.match(nested, /\n````$/)
})

test("clipTerminalTextForAi caps oversized scrollback without breaking surrogate pairs", () => {
  const short = clipTerminalTextForAi("hello\nworld", 200)
  assert.equal(short.truncated, false)
  assert.equal(short.text, "hello\nworld")

  // Place emoji surrogate pairs right across cut boundaries to ensure valid UTF-16/UTF-8.
  const huge = "HEAD_CMD\n" + "🚀".repeat(600) + "\nTAIL_ERROR: bind address already in use"
  const clipped = clipTerminalTextForAi(huge, 300)
  assert.equal(clipped.truncated, true)
  assert.ok(clipped.omittedChars > 0)
  assert.ok(clipped.text.length <= 320)
  assert.match(clipped.text, /^HEAD_CMD/)
  assert.match(clipped.text, /TAIL_ERROR: bind address already in use$/)
  assert.match(clipped.text, /中间省略 \d+ 字符/)
  // Fatal UTF-8 round-trip proves no lone surrogate was created.
  const encoded = new TextEncoder().encode(clipped.text)
  const decoded = new TextDecoder("utf-8", { fatal: true }).decode(encoded)
  assert.equal(decoded, clipped.text)
  assert.doesNotMatch(decoded, /\uFFFD/)
})

test("buildTerminalAiPrompt includes target, mode, cwd and stays well under the 64 KiB session limit", () => {
  assert.throws(
    () => buildTerminalAiPrompt({ selection: "   ", target: { cluster: "prod", instance: "web-01" } }),
    /请先在终端选中要发送的内容/,
  )

  const prompt = buildTerminalAiPrompt({
    selection: "$ systemctl status nginx\nActive: failed (Result: exit-code)",
    target: { cluster: "prod-cn", instance: "web-01" },
    cwd: "/etc/nginx",
    terminalMode: "pty",
  })
  assert.match(prompt, /prod-cn\/web-01 · 交互终端 · 目录 \/etc\/nginx/)
  assert.match(prompt, /```console\n\$ systemctl status nginx\nActive: failed \(Result: exit-code\)\n```/)

  const massive = "x".repeat(MAX_TERMINAL_AI_CHARS + 30_000)
  const massivePrompt = buildTerminalAiPrompt({
    selection: massive,
    target: { cluster: "prod-cn", instance: "web-01" },
    terminalMode: "command",
  })
  assert.ok(massivePrompt.length < 64_000, `prompt length ${massivePrompt.length} must stay below 64_000`)
  assert.match(massivePrompt, /已自动保留首尾并省略中间/)
})

test("buildTerminalComposerPrefill wraps terminal output with a trailing newline for appending", () => {
  const prefill = buildTerminalComposerPrefill("Error: ECONNREFUSED 127.0.0.1:5432")
  assert.equal(prefill, "```console\nError: ECONNREFUSED 127.0.0.1:5432\n```\n\n")
})

test("clampContextMenuPosition keeps the menu inside the viewport on every edge", () => {
  assert.deepEqual(
    clampContextMenuPosition({ x: 120, y: 160 }, { width: 260, height: 180 }, { width: 1280, height: 800 }),
    { left: 120, top: 160 },
  )
  const flipped = clampContextMenuPosition({ x: 1250, y: 760 }, { width: 260, height: 180 }, { width: 1280, height: 800 })
  assert.equal(flipped.left, 990)
  assert.equal(flipped.top, 580)

  const cramped = clampContextMenuPosition({ x: 2, y: 2 }, { width: 320, height: 240 }, { width: 300, height: 220 })
  assert.equal(cramped.left, 8)
  assert.equal(cramped.top, 8)
})

test("dispatchTerminalAiRequest opens a disconnected idle runtime and immediately sends the prompt", async () => {
  let opened = 0
  const sent = []
  const prefilled = []
  let connection = "disconnected"
  const runtime = {
    getSnapshot: () => ({ connection, status: "idle", items: [], interactions: [] }),
    async open() {
      opened += 1
      connection = "connected"
    },
    async sendMessage(sessionId, commandId, input) {
      sent.push({ sessionId, commandId, text: input.text })
    },
  }

  const result = await dispatchTerminalAiRequest({
    runtime,
    sessionId: "console-abc",
    target: { cluster: "prod", instance: "node-1" },
    request: { id: "req-1", mode: "execute", selection: "OOMKilled", cwd: "/var/log", terminalMode: "pty" },
    onPrefill: (p) => prefilled.push(p),
    createCommandId: () => "cmd-fixed-1",
  })

  assert.equal(result.outcome, "sent")
  assert.equal(opened, 1)
  assert.equal(prefilled.length, 0)
  assert.equal(sent.length, 1)
  assert.equal(sent[0].sessionId, "console-abc")
  assert.equal(sent[0].commandId, "cmd-fixed-1")
  assert.match(sent[0].text, /prod\/node-1 · 交互终端 · 目录 \/var\/log/)
  assert.match(sent[0].text, /OOMKilled/)

  // 真实 AgentWorkspace 首次挂载会把状态置为 connecting，且 open() resolve 后下一帧才发布 connected + sendMessage=true：
  // 首次冷启动右键「发送到 AI 执行」必须等待就绪后立即发出，不得误报 agent_workbench_session_not_ready。
  let delayedState = { connection: "connecting", status: "idle", capabilities: { sendMessage: false } }
  const listeners = new Set()
  const coldSent = []
  const coldRuntime = {
    getSnapshot: () => delayedState,
    subscribe: (_sid, fn) => {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    async open() {
      setTimeout(() => {
        delayedState = { connection: "connected", status: "idle", capabilities: { sendMessage: true } }
        for (const fn of listeners) fn()
      }, 25)
    },
    async sendMessage(_sid, _cid, input) {
      if (!delayedState.capabilities.sendMessage) throw new Error("agent_workbench_session_not_ready")
      coldSent.push(input.text)
    },
  }
  const coldResult = await dispatchTerminalAiRequest({
    runtime: coldRuntime,
    sessionId: "console-cold",
    target: { cluster: "prod", instance: "node-1" },
    request: { id: "req-cold", mode: "execute", selection: "Error: ImagePullBackOff", terminalMode: "command" },
    onPrefill: () => assert.fail("cold start should send directly once session becomes ready"),
  })
  assert.equal(coldResult.outcome, "sent")
  assert.equal(coldSent.length, 1)
})

test("dispatchTerminalAiRequest falls back to composer prefill when AI is busy or connection fails", async () => {
  const prefilled = []
  let sendCalled = false
  const busyRuntime = {
    getSnapshot: () => ({ connection: "connected", status: "running", items: [], interactions: [] }),
    async open() {},
    async sendMessage() { sendCalled = true },
  }

  const busyResult = await dispatchTerminalAiRequest({
    runtime: busyRuntime,
    sessionId: "console-busy",
    target: { cluster: "prod", instance: "node-1" },
    request: { id: "req-busy", mode: "execute", selection: "CrashLoopBackOff", terminalMode: "command" },
    onPrefill: (p) => prefilled.push(p),
  })
  assert.equal(busyResult.outcome, "prefilled_busy")
  assert.equal(sendCalled, false)
  assert.equal(prefilled.length, 1)
  assert.equal(prefilled[0].mode, "append")
  assert.match(prefilled[0].text, /CrashLoopBackOff/)

  const brokenPrefills = []
  const brokenRuntime = {
    getSnapshot: () => ({ connection: "disconnected", status: "idle", error: "该目标还没有绑定 DoAgent 模型" }),
    async open() { throw new Error("503 no model") },
    async sendMessage() { assert.fail("must not send when open failed") },
  }
  const errorResult = await dispatchTerminalAiRequest({
    runtime: brokenRuntime,
    sessionId: "console-nomodel",
    target: { cluster: "prod", instance: "node-1" },
    request: { id: "req-err", mode: "execute", selection: "nginx -t failed", terminalMode: "pty" },
    onPrefill: (p) => brokenPrefills.push(p),
  })
  assert.equal(errorResult.outcome, "prefilled_on_error")
  assert.match(errorResult.error, /该目标还没有绑定 DoAgent 模型/)
  assert.equal(brokenPrefills.length, 1)
  assert.match(brokenPrefills[0].text, /nginx -t failed/)

  const manualPrefills = []
  let bgOpened = 0
  const prefillOnlyResult = await dispatchTerminalAiRequest({
    runtime: {
      getSnapshot: () => ({ connection: "disconnected", status: "idle" }),
      async open() { bgOpened += 1 },
      async sendMessage() { assert.fail("prefill mode must not auto-send") },
    },
    sessionId: "console-prefill",
    target: { cluster: "prod", instance: "node-1" },
    request: { id: "req-pf", mode: "prefill", selection: "df -h", terminalMode: "pty" },
    onPrefill: (p) => manualPrefills.push(p),
  })
  assert.equal(prefillOnlyResult.outcome, "prefilled")
  assert.equal(bgOpened, 1)
  assert.equal(manualPrefills.length, 1)
})

test("terminal components wire right-click context menu, Shift bypass, PTY ResizeObserver and command prefill mode switch", () => {
  const menu = readFileSync(new URL("../components/terminal-context-menu.tsx", import.meta.url), "utf8")
  const pty = readFileSync(new URL("../components/terminal-pty.tsx", import.meta.url), "utf8")
  const panel = readFileSync(new URL("../components/terminal-panel.tsx", import.meta.url), "utf8")

  // 新菜单组件严禁硬编码十六进制色值，挂到 document.body 并使用 bg-popover 语义令牌。
  assert.doesNotMatch(menu, /#[0-9a-fA-F]{3,8}\b/)
  assert.match(menu, /createPortal\(/)
  assert.match(menu, /bg-popover/)
  assert.match(menu, /发送到 AI 执行/)
  assert.match(menu, /填入 AI 输入框/)
  assert.match(menu, /复制选中内容/)

  // 交互终端支持选中读取、Shift+右键保留原生菜单、分栏伸缩自动 fit。
  assert.match(pty, /if \(event\.shiftKey\) return/)
  assert.match(pty, /termRef\.current\?\.getSelection\(\)/)
  assert.match(pty, /new ResizeObserver/)
  assert.match(pty, /<TerminalContextMenu/)

  // 命令终端输出区同样接入右键菜单，且从 AI 助手“填入终端”时自动切到 command 页签并聚焦输入框。
  assert.match(panel, /setMode\("command"\)/)
  assert.match(panel, /getSelectionTextWithin\(logRef\.current\)/)
  assert.match(panel, /data-testid="terminal-ai-toggle"/)
  assert.match(panel, /<TerminalContextMenu/)
})
