import assert from "node:assert/strict"
import test from "node:test"
import {
  parseOsc7Cwd,
  ShellIntegrationTracker,
} from "./terminal-shell-integration.ts"

function createMockTerminal() {
  const oscHandlers = new Map()
  const lines = []
  let bufferType = "normal"
  let cursorY = 0
  let cursorX = 0
  let bufferChangeListener = null
  const markers = []

  const makeLine = (text, isWrapped = false) => ({
    isWrapped,
    translateToString(trimRight = true, startCol = 0, endCol = undefined) {
      const slice = text.slice(startCol, endCol)
      return trimRight ? slice.replace(/\s+$/, "") : slice
    },
  })

  const term = {
    buffer: {
      get active() {
        return {
          type: bufferType,
          baseY: 0,
          cursorY,
          cursorX,
          length: lines.length,
          getLine: (y) => lines[y],
        }
      },
      onBufferChange(listener) {
        bufferChangeListener = listener
        return { dispose() { bufferChangeListener = null } }
      },
    },
    parser: {
      registerOscHandler(ident, cb) {
        oscHandlers.set(ident, cb)
        return { dispose() { oscHandlers.delete(ident) } }
      },
    },
    registerMarker(offset = 0) {
      const m = {
        line: cursorY + offset,
        isDisposed: false,
        dispose() { this.isDisposed = true },
      }
      markers.push(m)
      return m
    },
    // Test helpers
    setLine(y, text, isWrapped = false) {
      lines[y] = makeLine(text, isWrapped)
    },
    moveCursor(y, x) {
      cursorY = y
      cursorX = x
    },
    emitOsc(ident, data) {
      oscHandlers.get(ident)?.(data)
    },
    setBufferType(next) {
      bufferType = next
      bufferChangeListener?.(term.buffer.active)
    },
    markers,
  }
  return term
}

test("parseOsc7Cwd extracts and decodes file:// paths", () => {
  assert.equal(parseOsc7Cwd("file://node-1/srv/my%20app"), "/srv/my app")
  assert.equal(parseOsc7Cwd("file://localhost/tmp"), "/tmp")
  assert.equal(parseOsc7Cwd("http://example.com/a"), null)
})

test("ShellIntegrationTracker tracks A/B/C/D blocks, wrapped commands, empty Enter, exit codes, and OSC 7", () => {
  const term = createMockTerminal()
  const started = []
  let nowVal = 1000
  const tracker = new ShellIntegrationTracker(term, {
    maxBlocks: 50,
    now: () => (nowVal += 100),
    onCommandStart: (b) => started.push(b.command),
  })

  // OSC 7 cwd
  term.emitOsc(7, "file://host-1/srv/app")
  assert.equal(tracker.cwd, "/srv/app")

  // Block 1: wrapped command + output + exit 0
  term.setLine(0, "$ kubectl get pods \\")
  term.setLine(1, "  -n web", true)
  term.moveCursor(0, 0)
  term.emitOsc(133, "A")
  term.moveCursor(0, 2)
  term.emitOsc(133, "B")
  assert.equal(tracker.atPrompt, true)
  term.moveCursor(1, 8)
  assert.equal(tracker.readCurrentInput(), "kubectl get pods \\  -n web")

  term.setLine(2, "nginx-1   1/1   Running")
  term.moveCursor(2, 0)
  term.emitOsc(133, "C")
  assert.equal(tracker.atPrompt, false)
  assert.equal(started[0], "kubectl get pods \\  -n web")

  term.moveCursor(3, 0)
  term.emitOsc(133, "D;0")

  const b1 = tracker.blocks[0]
  assert.equal(b1.state, "done")
  assert.equal(b1.exitCode, 0)
  assert.equal(b1.cwd, "/srv/app")
  assert.equal(tracker.readOutput(b1).text, "nginx-1   1/1   Running")

  // Empty Enter: A -> B -> D (no C) must be ignored
  term.setLine(3, "$ ")
  term.moveCursor(3, 0)
  term.emitOsc(133, "A")
  term.moveCursor(3, 2)
  term.emitOsc(133, "B")
  term.emitOsc(133, "D;0")
  assert.equal(tracker.blocks.length, 2)
  assert.equal(tracker.current.state, "prompt")

  // Block 2: AI comment prompt (# ...) + exit 130 interrupted
  term.setLine(4, "$ # 为什么 nginx 重启")
  term.moveCursor(4, 0)
  term.emitOsc(133, "A")
  term.moveCursor(4, 2)
  term.emitOsc(133, "B")
  term.moveCursor(5, 0)
  term.emitOsc(133, "C")
  assert.equal(tracker.current.isAiPrompt, true)
  term.emitOsc(133, "D;130")
  assert.equal(tracker.current.state, "interrupted")

  // Alternate screen pauses tracking
  term.setBufferType("alternate")
  assert.equal(tracker.inAlternateScreen, true)
  assert.equal(tracker.atPrompt, false)
  term.emitOsc(133, "A")
  assert.equal(tracker.blocks.length, 2)
  term.setBufferType("normal")
  assert.equal(tracker.inAlternateScreen, false)

  // Disposed marker reports trimmed output
  b1.commandMarker.dispose()
  const outAfterDispose = tracker.readOutput(b1)
  assert.equal(outAfterDispose.trimmed, true)

  tracker.dispose()
})

test("H1 regression: replay with suppressCallbacks rebuilds history blocks without firing onCommandStart", () => {
  const term = createMockTerminal()
  const fired = []
  const tracker = new ShellIntegrationTracker(term, {
    suppressCallbacks: true,
    onCommandStart: (b) => fired.push(b.command),
  })

  // 重放历史 # 提问
  term.setLine(0, "$ # 历史问题")
  term.moveCursor(0, 0)
  term.emitOsc(133, "A")
  term.moveCursor(0, 2)
  term.emitOsc(133, "B")
  term.moveCursor(1, 0)
  term.emitOsc(133, "C")
  term.emitOsc(133, "D;0")

  assert.equal(tracker.blocks.length, 1)
  assert.equal(tracker.blocks[0].isAiPrompt, true)
  assert.equal(fired.length, 0, "replay must not re-fire AI prompts")

  // 重放完成后解除静音：新提问正常触发
  tracker.setSuppressCallbacks(false)
  term.setLine(2, "$ # 新问题")
  term.moveCursor(2, 0)
  term.emitOsc(133, "A")
  term.moveCursor(2, 2)
  term.emitOsc(133, "B")
  term.moveCursor(3, 0)
  term.emitOsc(133, "C")
  assert.deepEqual(fired, ["# 新问题"])

  tracker.dispose()
})

test("H3 regression: multiline PS1 reads command strictly from B inputMarker line, not A prompt line", () => {
  const term = createMockTerminal()
  const tracker = new ShellIntegrationTracker(term)

  // A 在第 0 行，B 在第 1 行第 2 列
  term.setLine(0, "root@prod (main) /srv/app")
  term.setLine(1, "$ # 为什么 nginx 重启")
  term.moveCursor(0, 0)
  term.emitOsc(133, "A")
  term.moveCursor(1, 2)
  term.emitOsc(133, "B")
  term.moveCursor(1, 16)
  assert.equal(tracker.readCurrentInput(), "# 为什么 nginx 重启")

  term.moveCursor(2, 0)
  term.emitOsc(133, "C")
  assert.equal(tracker.current.command, "# 为什么 nginx 重启")
  assert.equal(tracker.current.isAiPrompt, true)

  tracker.dispose()
})

test("M4 degradation: repeated D without B degrades atPrompt so insert is not permanently locked", () => {
  const term = createMockTerminal()
  const tracker = new ShellIntegrationTracker(term)

  term.emitOsc(133, "D;0")
  assert.equal(tracker.promptTrackingDegraded, false)
  term.emitOsc(133, "D;0")
  assert.equal(tracker.promptTrackingDegraded, true)
  assert.equal(tracker.atPrompt, true)

  // 一旦 B 恢复，解除降级
  term.setLine(0, "$ ")
  term.moveCursor(0, 2)
  term.emitOsc(133, "B")
  assert.equal(tracker.promptTrackingDegraded, false)
  assert.equal(tracker.atPrompt, true)

  // recordAiBlock 显式登记 AI 提问块（因为 GNU Bash 对纯注释行不展开 PS0/OSC 133;C）
  const aiBlk = tracker.recordAiBlock("# 为什么 nginx 报错")
  assert.equal(aiBlk.isAiPrompt, true)
  assert.equal(aiBlk.aiStatus, "pending")
  assert.equal(aiBlk.command, "# 为什么 nginx 报错")
  tracker.markBlockAiStatus(aiBlk.id, "answered")
  assert.equal(aiBlk.aiStatus, "answered")

  tracker.dispose()
})

test("readCurrentPromptText captures single-line, multi-line OSC 133, and non-OSC prompts", () => {
  const term = createMockTerminal()
  const tracker = new ShellIntegrationTracker(term)

  // 无 OSC 133 时按当前光标行前导内容回退
  term.setLine(0, "root@doops-agent:/workspace# ")
  term.moveCursor(0, 29)
  assert.deepEqual(tracker.readCurrentPromptText(), {
    text: "root@doops-agent:/workspace# ",
    lines: 1,
  })

  // 多行 OSC 133 提示符（A 在 1 行，B 在 2 行第 2 列）
  term.setLine(1, "root@prod /root/ws/console-1")
  term.setLine(2, "# ")
  term.moveCursor(1, 0)
  term.emitOsc(133, "A")
  term.moveCursor(2, 2)
  term.emitOsc(133, "B")
  assert.deepEqual(tracker.readCurrentPromptText(), {
    text: "root@prod /root/ws/console-1\r\n# ",
    lines: 2,
  })

  tracker.dispose()
})
