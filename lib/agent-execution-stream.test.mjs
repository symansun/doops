import assert from "node:assert/strict"
import test from "node:test"
import {
  extractAgentExecutionEntries,
  formatExecutionDuration,
} from "./agent-execution-stream.ts"

test("extractAgentExecutionEntries extracts shell commands, exit codes, durations and non-shell tools in order", () => {
  const entries = extractAgentExecutionEntries([
    { id: "u1", kind: "message", title: "ignored" },
    {
      id: "t1",
      kind: "tool",
      sourceToolName: "Bash",
      identity: { namespace: "doagent", semanticKey: "shell.execute", sourceToolName: "Bash" },
      status: "completed",
      inputValue: { command: "kubectl get pods -n prod", cwd: "/root/ws/console-1", exitCode: 0 },
      output: "api-0   1/1   Running",
      timing: { durationMillis: 820 },
      execution: { turnId: "turn-1" },
    },
    {
      id: "t2",
      kind: "tool",
      sourceToolName: "Bash",
      identity: { namespace: "doagent", semanticKey: "shell.execute", sourceToolName: "Bash" },
      status: "failed",
      input: JSON.stringify({ cmd: "kubectl logs api-0 --previous", exitCode: 1 }),
      failure: { message: "previous container not found" },
      timing: { startedAt: "2026-11-28T10:00:00Z", finishedAt: "2026-11-28T10:00:02.500Z" },
    },
    {
      id: "t3",
      kind: "tool",
      sourceToolName: "Read",
      identity: { namespace: "doagent", semanticKey: "filesystem.read", sourceToolName: "Read" },
      status: "completed",
      inputValue: { path: "/etc/nginx/nginx.conf" },
      results: [{ kind: "text", text: "worker_processes auto;" }],
    },
  ])

  assert.equal(entries.length, 3)

  assert.equal(entries[0].index, 1)
  assert.equal(entries[0].isShell, true)
  assert.equal(entries[0].command, "kubectl get pods -n prod")
  assert.equal(entries[0].cwd, "/root/ws/console-1")
  assert.equal(entries[0].exitCode, 0)
  assert.equal(entries[0].durationMs, 820)
  assert.equal(entries[0].output, "api-0   1/1   Running")

  assert.equal(entries[1].index, 2)
  assert.equal(entries[1].isShell, true)
  assert.equal(entries[1].command, "kubectl logs api-0 --previous")
  assert.equal(entries[1].exitCode, 1)
  assert.equal(entries[1].status, "failed")
  assert.equal(entries[1].durationMs, 2500)
  assert.equal(entries[1].output, "previous container not found")

  assert.equal(entries[2].index, 3)
  assert.equal(entries[2].isShell, false)
  assert.equal(entries[2].command, undefined)
  assert.equal(entries[2].summary, "/etc/nginx/nginx.conf")
  assert.equal(entries[2].output, "worker_processes auto;")
})

test("formatExecutionDuration formats milliseconds, seconds and minutes cleanly", () => {
  assert.equal(formatExecutionDuration(undefined), "")
  assert.equal(formatExecutionDuration(240), "240ms")
  assert.equal(formatExecutionDuration(1450), "1.5s")
  assert.equal(formatExecutionDuration(12400), "12s")
  assert.equal(formatExecutionDuration(75000), "1m15s")
})
