import assert from "node:assert/strict"
import test from "node:test"
import {
  addAgentRunNote,
  addAgentRunOutput,
  addAgentRunResult,
  agentRunTraceLines,
  mergeResultText,
  startAgentRun,
} from "./agent-output.ts"

const header = "目标 test/node · 扫描发现"
const answer = "## 发现 2 个数据库实例\n\n| 引擎 | 服务 | 端口 |\n| --- | --- | --- |\n| mysql | db | 3306 |\n\n**未凭猜测补全。**"

function replay(events) {
  let run = startAgentRun(header)
  for (const [type, kind, data] of events) {
    if (type === "output") run = addAgentRunOutput(run, kind, data)
    else if (type === "result") run = addAgentRunResult(run, data)
    else run = addAgentRunNote(run, { kind, text: data })
  }
  return run
}

test("agent run events split into trace, answer and plain error text", () => {
  for (const { name, events, trace, answer: wantAnswer, errors = [] } of [
    {
      name: "DoSql run shows tool trace apart and the answer once",
      events: [
        ["output", "tool", "[tool:Skill]"],
        ["output", "tool", "[tool:mcp_dosql_read]"],
        ["output", "assistant_delta", answer.slice(0, 20)],
        ["output", "assistant_delta", answer.slice(20)],
        ["result", undefined, answer],
      ],
      trace: ["[tool:Skill]", "[tool:mcp_dosql_read]"],
      answer,
    },
    {
      name: "raw progress split across chunks joins into whole lines",
      events: [
        ["output", undefined, "[plan] 收到"],
        ["output", "raw", "任务\n[step 1/3] 采集\n"],
        ["output", "tool", "[tool:Skill]"],
      ],
      trace: ["[plan] 收到任务", "[step 1/3] 采集", "[tool:Skill]"],
      answer: "",
    },
    {
      name: "tool line after an unterminated raw chunk starts a new line",
      events: [["output", "raw", "[warning] Helm mutation was verified"], ["output", "tool", "[tool:mcp_doops_helm]"]],
      trace: ["[warning] Helm mutation was verified", "[tool:mcp_doops_helm]"],
      answer: "",
    },
    {
      name: "answer chunks stay one Markdown text, not trace lines",
      events: [["output", "assistant_delta", "**发现** 2 个"], ["output", "assistant_delta", "实例\n| a | b |"]],
      trace: [],
      answer: "**发现** 2 个实例\n| a | b |",
    },
    {
      name: "result without a stream becomes the answer",
      events: [["output", "tool", "[tool:Skill]"], ["result", undefined, answer]],
      trace: ["[tool:Skill]"],
      answer,
    },
    {
      name: "empty result keeps the streamed answer",
      events: [["output", "assistant_delta", answer], ["result", undefined, ""]],
      trace: [],
      answer,
    },
    {
      name: "empty run has no trace and no answer",
      events: [["output", "tool", ""], ["output", "raw", ""], ["result", undefined, "  "]],
      trace: [],
      answer: "",
    },
    {
      name: "error events and failures stay plain text notes",
      events: [
        ["output", "tool", "[tool:mcp_dosql_read]"],
        ["output", "error", "[error] doagent_message_too_large\n"],
        ["output", "error", "  "],
        ["note", "err", "dosql_query_failed"],
      ],
      trace: ["[tool:mcp_dosql_read]"],
      answer: "",
      errors: ["[error] doagent_message_too_large", "dosql_query_failed"],
    },
  ]) {
    const run = replay(events)
    assert.deepEqual(agentRunTraceLines(run), trace, name)
    assert.equal(run.answer.trim(), wantAnswer, name)
    assert.deepEqual(run.notes.filter((note) => note.kind === "err").map((note) => note.text), errors, name)
    assert.deepEqual(run.notes[0], { kind: "info", text: header }, name)
  }
})

test("final result is kept once next to the streamed answer", () => {
  for (const [name, streamed, result, want] of [
    ["no stream shows the whole result", "", answer, answer],
    ["result equal to the stream is not repeated", answer, answer, answer],
    ["whitespace differences do not repeat it", `${answer}\n`, `\n${answer}`, answer],
    ["stream ending with the result is not repeated", `前置说明\n${answer}`, answer, `前置说明\n${answer}`],
    ["result extending the stream replaces it", "## 发现 2 个", answer, answer],
    ["unrelated result follows as a new paragraph", "partial", "other answer", "partial\n\nother answer"],
    ["empty result keeps the stream", answer, "", answer],
    ["completion marker after an answer is not appended", answer, "Operation complete.", answer],
    ["completion marker without an answer is shown", "", "Operation complete.", "Operation complete."],
    ["empty stream and result stay empty", "", "", ""],
  ]) {
    assert.equal(mergeResultText(streamed, result), want, name)
  }
})
