import assert from "node:assert/strict"
import test from "node:test"
import { unseenResultText } from "./agent-output.ts"

test("final result shows only text the assistant stream has not shown", () => {
  const answer = "发现 2 个数据库实例：\n| mysql | db | 3306 |\n未凭猜测补全。"
  for (const [name, streamed, result, want] of [
    ["no stream shows the whole result", "", answer, answer],
    ["result equal to the stream is not repeated", answer, answer, ""],
    ["whitespace differences do not repeat it", `${answer}\n`, `\n${answer}`, ""],
    ["stream ending with the result is not repeated", `前置说明\n${answer}`, answer, ""],
    ["result extending the stream shows only the rest", "发现 2 个", answer, answer.slice("发现 2 个".length).trim()],
    ["unrelated result is kept", "partial", "other answer", "other answer"],
    ["empty result shows nothing", answer, "", ""],
  ]) {
    assert.equal(unseenResultText(streamed, result), want, name)
  }
})
