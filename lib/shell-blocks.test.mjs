import assert from "node:assert/strict"
import test from "node:test"
import {
  appendShellOutput,
  describeShellFailure,
  destructiveCommandReason,
  execGate,
  extractShellBlocks,
  latestAssistantShellBlocks,
  nextTerminalPrefill,
  prefillToApply,
  shellResultOutcome,
  shellToolArguments,
  targetSwitchBlocker,
  terminalExecGate,
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
    { name: "bash transcript keeps prompt lines, drops output", input: "```bash\n$ hostname\nmaster-1\n$ df -h / | tail -1\n/dev/vda1 40G 12G 28G 30% /\n```", want: ["hostname\ndf -h / | tail -1"] },
    { name: "sh with prompt only", input: "```sh\n$ uptime\n```", want: ["uptime"] },
    { name: "indented prompt", input: "```shell\n  $ id\n  uid=0(root)\n```", want: ["id"] },
    { name: "bash comments without prompts stay", input: "```bash\n# 查看磁盘\ndf -h\n```", want: ["# 查看磁盘\ndf -h"] },
    { name: "mixed languages keep shell order", input: "```yaml\na: 1\n```\n```bash\nls\n```\n```json\n{\"rm\": 1}\n```\n```shell\npwd\n```", want: ["ls", "pwd"] },
    { name: "console without prompts yields nothing", input: "```console\nroot\n```", want: [] },
    { name: "prompt continuation line kept", input: "```bash\n$ kubectl get pods \\\n>   -A\nNAME READY\n```", want: ["kubectl get pods \\\n  -A"] },
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
    { name: "dedupes after prompt normalization", items: [assistant("```console\n$ ls\n```\n```bash\nls\n```\n```bash\n$ ls\nfile\n```")], want: ["ls"] },
    { name: "failed message shows nothing", items: [assistant("```bash\nls\n```", "failed")], want: [] },
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

test("destructiveCommandReason flags commands that delete, stop or overwrite", () => {
  const cases = [
    { command: "ls -la /var/log", destructive: false },
    { command: "hostname && df -h / | tail -1", destructive: false },
    { command: "kubectl get pods -A", destructive: false },
    { command: "kubectl logs deploy/api | grep delete", destructive: false },
    { command: "helm list -A", destructive: false },
    { command: "docker ps -a", destructive: false },
    { command: "systemctl status nginx", destructive: false },
    { command: "grep -r rm /etc/cron.d", destructive: false },
    { command: "cat /var/log/rm.log", destructive: false },
    { command: "git status", destructive: false },
    { command: "find /var/log -name '*.gz'", destructive: false },
    { command: "rm -rf /tmp/build", destructive: true },
    { command: "sudo rm /etc/nginx/conf.d/a.conf", destructive: true },
    { command: "FOO=1 rm x", destructive: true },
    { command: "cd /tmp && rm -r build", destructive: true },
    { command: "ls *.log | xargs rm -f", destructive: true },
    { command: "find /var/log -name '*.gz' -delete", destructive: true },
    { command: "find . -name core -exec rm {} \\;", destructive: true },
    { command: "kubectl delete pod api-0", destructive: true },
    { command: "kubectl -n prod delete deploy api", destructive: true },
    { command: "kubectl drain node-1 --ignore-daemonsets", destructive: true },
    { command: "helm uninstall api -n prod", destructive: true },
    { command: "helm -n prod rollback api 3", destructive: true },
    { command: "docker rm -f web", destructive: true },
    { command: "docker system prune -a", destructive: true },
    { command: "docker compose down", destructive: true },
    { command: "systemctl restart nginx", destructive: true },
    { command: "service nginx stop", destructive: true },
    { command: "reboot", destructive: true },
    { command: "shutdown -h now", destructive: true },
    { command: "mkfs.ext4 /dev/vdb", destructive: true },
    { command: "dd if=/dev/zero of=/dev/vdb bs=1M", destructive: true },
    { command: "kill -9 1234", destructive: true },
    { command: "pkill nginx", destructive: true },
    { command: "truncate -s 0 /var/log/app.log", destructive: true },
    { command: "psql -c 'DROP TABLE users'", destructive: true },
    { command: "mysql -e \"delete from orders\"", destructive: true },
    { command: "git reset --hard HEAD~1", destructive: true },
    { command: "git push --force origin main", destructive: true },
    { command: "crontab -r", destructive: true },
    { command: "chmod -R 777 /srv", destructive: true },
    { command: "ls\nrm -rf /tmp/x", destructive: true },
  ]
  for (const c of cases) {
    const reason = destructiveCommandReason(c.command)
    if (c.destructive) assert.ok(reason && reason.length > 0, `expected destructive: ${c.command}`)
    else assert.equal(reason, null, `expected safe: ${c.command}`)
  }
})

test("execGate asks once for destructive commands and resets when the command changes", () => {
  const cases = [
    { name: "safe command runs", command: "hostname", confirmed: "", want: "run" },
    { name: "destructive needs confirm", command: "rm -rf /tmp/x", confirmed: "", want: "confirm" },
    { name: "confirmed same command runs", command: "rm -rf /tmp/x", confirmed: "rm -rf /tmp/x", want: "run" },
    { name: "confirmation ignores outer whitespace", command: " rm -rf /tmp/x\n", confirmed: "rm -rf /tmp/x", want: "run" },
    { name: "edited after confirm asks again", command: "rm -rf /tmp/y", confirmed: "rm -rf /tmp/x", want: "confirm" },
    { name: "empty command does nothing", command: "  ", confirmed: "", want: "empty" },
  ]
  for (const c of cases) {
    assert.equal(execGate(c.command, c.confirmed), c.want, c.name)
  }
})

test("terminalExecGate confirms only the unedited destructive assistant command", () => {
  const cases = [
    { name: "typed destructive runs as before", command: "rm -rf /tmp/x", assistant: "", confirmed: "", want: "run" },
    { name: "assistant safe command runs", command: "hostname", assistant: "hostname", confirmed: "", want: "run" },
    { name: "assistant destructive needs confirm", command: "kubectl delete pod api-0", assistant: "kubectl delete pod api-0", confirmed: "", want: "confirm" },
    { name: "assistant destructive confirmed", command: "kubectl delete pod api-0", assistant: "kubectl delete pod api-0", confirmed: "kubectl delete pod api-0", want: "run" },
    { name: "edited by user runs", command: "kubectl delete pod api-1", assistant: "kubectl delete pod api-0", confirmed: "", want: "run" },
    { name: "empty", command: " ", assistant: "rm x", confirmed: "", want: "empty" },
  ]
  for (const c of cases) {
    assert.equal(terminalExecGate(c.command, { assistantCommand: c.assistant, confirmed: c.confirmed }), c.want, c.name)
  }
})

test("appendShellOutput keeps the Agent heartbeat out of command output", () => {
  const heartbeat = (s) => `\r\u001b[K[agent] ⏳ 命令后台执行中... (耗时 ${s}s)`
  const empty = { output: "", progress: "" }
  const cases = [
    { name: "plain line", chunks: ["master-1\n"], want: { output: "master-1\n", progress: "" } },
    { name: "heartbeat becomes progress", chunks: [heartbeat(5)], want: { output: "", progress: "[agent] ⏳ 命令后台执行中... (耗时 5s)" } },
    { name: "later heartbeat replaces earlier", chunks: [heartbeat(5), heartbeat(10)], want: { output: "", progress: "[agent] ⏳ 命令后台执行中... (耗时 10s)" } },
    { name: "output between heartbeats", chunks: ["a\n", heartbeat(5), "b\n"], want: { output: "a\nb\n", progress: "[agent] ⏳ 命令后台执行中... (耗时 5s)" } },
    { name: "ANSI colors stripped", chunks: ["\u001b[01;34mdir\u001b[0m\n"], want: { output: "dir\n", progress: "" } },
  ]
  for (const c of cases) {
    assert.deepEqual(c.chunks.reduce(appendShellOutput, empty), c.want, c.name)
  }
})

test("shellResultOutcome separates the success marker from start failures", () => {
  const cases = [
    { name: "success marker", text: "Operation complete.", want: { ok: true, text: "" } },
    { name: "start failure reported as success text", text: "Error starting command: exec: \"bash\": not found", want: { ok: false, text: "Error starting command: exec: \"bash\": not found" } },
    { name: "other text kept", text: "done\n", want: { ok: true, text: "done\n" } },
    { name: "empty", text: "", want: { ok: true, text: "" } },
  ]
  for (const c of cases) {
    assert.deepEqual(shellResultOutcome(c.text), c.want, c.name)
  }
})

test("describeShellFailure shows exit codes, stops and access problems in plain terms", () => {
  const target = { cluster: "doops-114-k8s", instance: "master-1" }
  const cases = [
    { name: "exit code", message: "Command failed with error: exit status 2", stop: false, kind: "exit", exitCode: 2, text: /退出码 2/ },
    { name: "signal", message: "Command failed with error: signal: killed", stop: false, kind: "signal", text: /killed/ },
    { name: "user stop", message: "operation canceled", stop: true, kind: "canceled", text: /已停止/ },
    { name: "cancel before dispatch", message: "请求派发前已停止", stop: true, kind: "canceled", text: /已停止/ },
    { name: "cancel or timeout without stop", message: "operation canceled", stop: false, kind: "canceled", text: /超过执行时限/ },
    { name: "forbidden", message: "forbidden: exec on doops-114-k8s/master-1", stop: false, kind: "forbidden", text: /无权.*doops-114-k8s\/master-1.*授权/ },
    { name: "offline", message: "target offline: doops-114-k8s/master-1", stop: false, kind: "offline", text: /目标离线.*doops-114-k8s\/master-1/ },
    { name: "auth expired", message: "gateway_auth_failed", stop: false, kind: "auth", text: /重新登录/ },
    { name: "no gateway session", message: "未建立 Gateway 会话", stop: false, kind: "auth", text: /重新登录/ },
    { name: "outcome unknown", message: "execution_outcome_unknown：远端终态未确认", stop: false, kind: "unknown", text: /核对实际状态/ },
    { name: "blocked by agent", message: "blocked_command(delete_root_filesystem): deleting the root filesystem is never allowed", stop: false, kind: "blocked", text: /Agent 拒绝执行.*delete_root_filesystem/ },
    { name: "start failure", message: "Error starting command: fork failed", stop: false, kind: "other", text: /fork failed/ },
  ]
  for (const c of cases) {
    const got = describeShellFailure(c.message, { stopRequested: c.stop, target })
    assert.equal(got.kind, c.kind, c.name)
    assert.match(got.text, c.text, c.name)
    if (c.exitCode !== undefined) assert.equal(got.exitCode, c.exitCode, c.name)
  }
})

test("targetSwitchBlocker keeps the target while a command or task is still running", () => {
  const idle = { skillRunning: false, agentBusy: false, execRunning: false }
  const cases = [
    { name: "idle allows switch", state: idle, want: null },
    { name: "skill running", state: { ...idle, skillRunning: true }, want: /等待当前操作结束/ },
    { name: "AI task running", state: { ...idle, agentBusy: true }, want: /停止当前 AI 任务/ },
    { name: "exec bar command running", state: { ...idle, execRunning: true }, want: /执行栏.*命令.*结束或停止/ },
    { name: "skill wins over exec", state: { skillRunning: true, agentBusy: false, execRunning: true }, want: /等待当前操作结束/ },
    { name: "AI task wins over exec", state: { skillRunning: false, agentBusy: true, execRunning: true }, want: /停止当前 AI 任务/ },
  ]
  for (const c of cases) {
    const got = targetSwitchBlocker(c.state)
    if (c.want === null) assert.equal(got, null, c.name)
    else assert.match(got ?? "", c.want, c.name)
  }
})
