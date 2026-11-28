import assert from "node:assert/strict"
import test from "node:test"
import {
  buildAiMirrorWritePayload,
  canFlushAiMirrorToPty,
  formatAiMirrorBlock,
  isMirrorableAiEntry,
  sanitizeAiMirrorOutput,
  selectPendingAiMirrorEntries,
  stripUnsafeTerminalEscapes,
} from "./terminal-ai-mirror.ts"

test("stripUnsafeTerminalEscapes preserves SGR colors but strips OSC/clear/alternate-screen escapes", () => {
  const dangerous =
    "\x1b]133;D;0\x07" +
    "\x1b]7;file://host/evil\x1b\\" +
    "\x1b[?1049h" +
    "\x1b[2J" +
    "\x1b[32mOK\x1b[0m\r\n" +
    "plain\x07line"
  const clean = stripUnsafeTerminalEscapes(dangerous)
  assert.equal(clean.includes("133;D"), false)
  assert.equal(clean.includes("file://host/evil"), false)
  assert.equal(clean.includes("1049h"), false)
  assert.equal(clean.includes("[2J"), false)
  assert.equal(clean, "\x1b[32mOK\x1b[0m\nplainline")
})

test("sanitizeAiMirrorOutput truncates excess lines and characters cleanly", () => {
  const manyLines = Array.from({ length: 12 }, (_, idx) => `line-${idx + 1}`).join("\n")
  const clipped = sanitizeAiMirrorOutput(manyLines, { maxLines: 5, maxChars: 1000 })
  assert.equal(clipped.truncated, true)
  assert.equal(clipped.omittedLines, 7)
  assert.equal(clipped.text.split("\n").length, 5)
  assert.equal(clipped.text.startsWith("line-1\n"), true)
})

test("selectPendingAiMirrorEntries picks only completed shell entries once and surfaces active running tool", () => {
  const entries = [
    {
      id: "t1",
      index: 1,
      toolName: "Read",
      semanticKey: "filesystem.read",
      isShell: false,
      summary: "SKILL.md",
      status: "completed",
      output: "doc",
    },
    {
      id: "t2",
      index: 2,
      toolName: "Bash",
      semanticKey: "shell.execute",
      isShell: true,
      command: "df -h",
      cwd: "/root/ws/console-1",
      exitCode: 0,
      durationMs: 180,
      status: "completed",
      output: "/dev/sda1 40%",
    },
    {
      id: "t3",
      index: 3,
      toolName: "Bash",
      semanticKey: "shell.execute",
      isShell: true,
      command: "du -sh /var/log",
      status: "running",
      output: "",
    },
  ]

  const first = selectPendingAiMirrorEntries(entries, new Set())
  assert.equal(first.shellTotal, 2)
  assert.deepEqual(first.readyToMirror.map((e) => e.id), ["t2"])
  assert.equal(first.activeRunning?.id, "t3")

  const second = selectPendingAiMirrorEntries(entries, new Set(["t2"]))
  assert.equal(second.readyToMirror.length, 0)
  assert.equal(second.activeRunning?.id, "t3")
  assert.equal(isMirrorableAiEntry(entries[0]), false)
  assert.equal(isMirrorableAiEntry(entries[2]), false)
})

test("canFlushAiMirrorToPty blocks writes when typing, in alternate screen, busy, or disabled", () => {
  const base = {
    enabled: true,
    status: "ready",
    initialPromptReady: true,
    inAlternateScreen: false,
    atPrompt: true,
    currentInput: "",
    optimisticInput: "",
  }
  assert.equal(canFlushAiMirrorToPty(base), true)
  assert.equal(canFlushAiMirrorToPty({ ...base, enabled: false }), false)
  assert.equal(canFlushAiMirrorToPty({ ...base, status: "connecting" }), false)
  assert.equal(canFlushAiMirrorToPty({ ...base, initialPromptReady: false }), false)
  assert.equal(canFlushAiMirrorToPty({ ...base, inAlternateScreen: true }), false)
  assert.equal(canFlushAiMirrorToPty({ ...base, atPrompt: false }), false)
  assert.equal(canFlushAiMirrorToPty({ ...base, currentInput: "kubectl " }), false)
  assert.equal(canFlushAiMirrorToPty({ ...base, optimisticInput: "ls" }), false)
})

test("formatAiMirrorBlock and buildAiMirrorWritePayload preserve OSC 133 prompt anchors without synthetic OSC on plain shells", () => {
  const block = formatAiMirrorBlock({
    id: "t2",
    index: 2,
    toolName: "Bash",
    semanticKey: "shell.execute",
    isShell: true,
    command: "uptime",
    cwd: "/root/ws/console-1",
    exitCode: 0,
    durationMs: 240,
    status: "completed",
    output: "15:33:50 up 10 days",
  })
  assert.match(block, /AI · Bash/)
  assert.match(block, /\/root\/ws\/console-1/)
  assert.match(block, /\$\x1b\[0m \x1b\[1muptime/)
  assert.match(block, /15:33:50 up 10 days/)
  assert.match(block, /✓ exit 0 · 240ms/)

  const oscPayload = buildAiMirrorWritePayload([block], {
    text: "root@agent:/workspace# ",
    lines: 1,
    seenOsc133: true,
  })
  assert.equal(oscPayload.startsWith("\r\x1b[2K"), true)
  assert.equal(oscPayload.endsWith("\x1b]133;A\x07root@agent:/workspace# \x1b]133;B\x07"), true)

  const plainPayload = buildAiMirrorWritePayload([block], {
    text: "# ",
    lines: 1,
    seenOsc133: false,
  })
  assert.equal(plainPayload.includes("\x1b]133;"), false)
  assert.equal(plainPayload.endsWith("# "), true)
})
