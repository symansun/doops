import assert from "node:assert/strict"
import test from "node:test"
import {
  extractShellBlocks,
  latestAssistantShellBlocks,
  nextTerminalPrefill,
  prefillToApply,
  shellToolArguments,
} from "./shell-blocks.ts"

test("extractShellBlocks returns only closed shell fences", () => {
  const cases = [
    { name: "bash", input: "看一下：\n```bash\nhostname && df -h / | tail -1\n```\n", want: ["hostname && df -h / | tail -1"] },
    { name: "sh and shell", input: "```sh\nuptime\n```\n```shell\nfree -m\n```", want: ["uptime", "free -m"] },
    { name: "case-insensitive language", input: "```Bash\nls\n```", want: ["ls"] },
    { name: "multi-line keeps lines", input: "```bash\ncd /tmp\nls -la\n```", want: ["cd /tmp\nls -la"] },
    { name: "tilde fence", input: "~~~bash\nid\n~~~", want: ["id"] },
    { name: "longer fence holds inner backticks", input: "````bash\necho '```'\n````", want: ["echo '```'"] },
    { name: "console keeps prompt lines only", input: "```console\n$ whoami\nroot\n$ pwd\n/root\n```", want: ["whoami\npwd"] },
    { name: "CRLF", input: "```bash\r\nhostname\r\n```\r\n", want: ["hostname"] },
    { name: "non-shell language ignored", input: "```yaml\nkey: value\n```\n```python\nprint(1)\n```", want: [] },
    { name: "untagged fence ignored", input: "```\nrm -rf /tmp/x\n```", want: [] },
    { name: "unclosed fence ignored", input: "```bash\nhostname", want: [] },
    { name: "empty block ignored", input: "```bash\n\n```", want: [] },
    { name: "inline code ignored", input: "运行 `hostname` 即可", want: [] },
  ]
  for (const c of cases) {
    assert.deepEqual(extractShellBlocks(c.input).map((b) => b.command), c.want, c.name)
  }
})

test("latestAssistantShellBlocks reads the newest completed assistant message", () => {
  const assistant = (text, status = "completed") => ({ kind: "message", role: "assistant", text, status })
  const cases = [
    { name: "empty", items: [], want: [] },
    { name: "latest assistant wins", items: [assistant("```bash\nold\n```"), { kind: "message", role: "user", text: "再来", status: "completed" }, assistant("```bash\nnew\n```")], want: ["new"] },
    { name: "skips trailing tool items", items: [assistant("```sh\nuptime\n```"), { kind: "tool", status: "completed" }], want: ["uptime"] },
    { name: "streaming message shows nothing", items: [assistant("```bash\nold\n```"), assistant("```bash\nhalf\n```", "streaming")], want: [] },
    { name: "dedupes repeated commands", items: [assistant("```bash\nls\n```\n```sh\nls\n```")], want: ["ls"] },
    { name: "user-only history", items: [{ kind: "message", role: "user", text: "```bash\nls\n```", status: "completed" }], want: [] },
  ]
  for (const c of cases) {
    assert.deepEqual(latestAssistantShellBlocks(c.items).map((b) => b.command), c.want, c.name)
  }
})

test("shellToolArguments sends only the session and the confirmed command", () => {
  const cases = [
    { name: "trims", session: "s1", command: "  hostname \n", want: { session_id: "s1", command: "hostname" } },
    { name: "multi-line", session: "s1", command: "cd /tmp\nls", want: { session_id: "s1", command: "cd /tmp\nls" } },
    { name: "empty command", session: "s1", command: "  ", error: /命令为空/ },
    { name: "missing session", session: "", command: "ls", error: /缺少会话/ },
  ]
  for (const c of cases) {
    if (c.error) {
      assert.throws(() => shellToolArguments(c.session, c.command), c.error, c.name)
      continue
    }
    const args = shellToolArguments(c.session, c.command)
    assert.deepEqual(args, c.want, c.name)
    assert.deepEqual(Object.keys(args).sort(), ["command", "session_id"], c.name)
  }
})

test("terminal prefill crosses tabs once per click and never auto-runs", () => {
  const first = nextTerminalPrefill(null, " hostname ")
  const cases = [
    { name: "first click", prefill: first, applied: 0, want: "hostname" },
    { name: "already applied", prefill: first, applied: first.nonce, want: null },
    { name: "same command clicked again", prefill: nextTerminalPrefill(first, "hostname"), applied: first.nonce, want: "hostname" },
    { name: "no prefill", prefill: null, applied: 0, want: null },
    { name: "blank command keeps previous", prefill: nextTerminalPrefill(first, "   "), applied: first.nonce, want: null },
  ]
  for (const c of cases) {
    assert.equal(prefillToApply(c.prefill, c.applied), c.want, c.name)
  }
  assert.equal(nextTerminalPrefill(first, "   "), first)
})
