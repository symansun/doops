// A DoSql or DoWeb run is one doops_agent_prompt call. Its stream mixes tool
// trace lines ([tool:Skill]), the assistant answer (Markdown) and errors, and
// the final result repeats the streamed answer. The pages used to print all of
// it as one list of <pre> lines, so tables showed as pipes between trace lines.

export interface AgentRunNote {
  kind: "info" | "err"
  text: string
}

export interface AgentRun {
  notes: AgentRunNote[]
  trace: string
  answer: string
}

export function startAgentRun(header: string): AgentRun {
  return { notes: [{ kind: "info", text: header }], trace: "", answer: "" }
}

export function addAgentRunNote(run: AgentRun, note: AgentRunNote): AgentRun {
  return { ...run, notes: [...run.notes, note] }
}

// Only assistant_delta is the answer. Tool and raw progress events are trace;
// error events stay plain text.
export function addAgentRunOutput(run: AgentRun, kind: string | undefined, data: string): AgentRun {
  if (!data) return run
  if (kind === "assistant_delta") return { ...run, answer: run.answer + data }
  if (kind === "error") return data.trim() ? addAgentRunNote(run, { kind: "err", text: data.trim() }) : run
  if (kind === "tool") {
    const separator = run.trace && !run.trace.endsWith("\n") ? "\n" : ""
    return { ...run, trace: `${run.trace}${separator}${data.trim()}\n` }
  }
  // Raw progress arrives in chunks that can end mid-line.
  return { ...run, trace: run.trace + data }
}

export function addAgentRunResult(run: AgentRun, result: string): AgentRun {
  return { ...run, answer: mergeResultText(run.answer, result) }
}

export function agentRunTraceLines(run: AgentRun): string[] {
  return run.trace.split("\n").map((line) => line.trimEnd()).filter((line) => line.trim())
}

// Older Agents and the demo end an answered turn with this marker.
const OPERATION_COMPLETE = "Operation complete."

// The final result usually repeats the streamed answer, whole or extended.
// Showing both printed the answer twice; keep the full text once.
export function mergeResultText(streamed: string, result: string): string {
  const final = result.trim()
  const shown = streamed.trim()
  if (!final || (shown && final === OPERATION_COMPLETE)) return streamed
  if (!shown || final.startsWith(shown)) return final
  if (shown.endsWith(final)) return shown
  return `${shown}\n\n${final}`
}
