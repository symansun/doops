// The final result of doops_agent_prompt repeats the assistant text already
// streamed as assistant_delta events. Showing both printed the answer twice.
// Returns only the part of the result that the stream has not shown yet.
export function unseenResultText(streamedAssistant: string, result: string): string {
  const final = result.trim()
  const streamed = streamedAssistant.trim()
  if (!final || !streamed) return final
  if (streamed.endsWith(final)) return ""
  if (final.startsWith(streamed)) return final.slice(streamed.length).trim()
  return final
}
