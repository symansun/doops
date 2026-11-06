import assert from "node:assert/strict"
import test from "node:test"
import {
  CommandJournal,
  UNLOAD_WARNING,
  beforeUnloadWarning,
  clearConsoleState,
  commandSummary,
  interruptedCommandNotice,
} from "./console-persistence.ts"

const scope = { gateway: "https://doops.l8ai.cn", username: "ops-zhang" }
const CANARY = "doops-synthetic-canary-k25-9c4d"
const NOW = new Date("2026-11-06T03:20:00.000Z")

function memoryStorage() {
  const data = new Map()
  return {
    data,
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: (key) => data.delete(key),
  }
}

function journal(storage) {
  return new CommandJournal(() => storage, () => NOW)
}

const command = { targetKey: "doops-zyj/zyj", sessionId: "console-abc12345", command: "sleep 20; echo qa-e1-done", source: "terminal" }

test("Given a command that ends normally, When it is journaled, Then its record is cleared", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  const id = commands.begin(scope, command)
  assert.equal(commands.activeCount(), 1)
  const [stored] = JSON.parse([...storage.data.values()][0])
  assert.deepEqual(Object.keys(stored).sort(), ["id", "sessionId", "source", "startedAt", "summary", "targetKey"])
  assert.equal(stored.summary, "sleep …")
  assert.equal(stored.startedAt, NOW.toISOString())
  commands.end(scope, id)
  assert.equal(commands.activeCount(), 0)
  assert.equal(storage.data.size, 0)
  assert.deepEqual(journal(storage).takeInterrupted(scope), [])
})

test("Given two commands in flight, When one ends, Then only the other remains", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  const first = commands.begin(scope, command)
  commands.begin(scope, { ...command, command: "uptime", source: "exec" })
  commands.end(scope, first)
  assert.equal(commands.activeCount(), 1)
  assert.deepEqual(journal(storage).takeInterrupted(scope).map((record) => [record.summary, record.source]), [["uptime", "exec"]])
})

test("Given a record left by a reload, When the new page takes it, Then it is returned once", () => {
  const storage = memoryStorage()
  journal(storage).begin(scope, command)
  const reloaded = journal(storage)
  const [record] = reloaded.takeInterrupted(scope)
  assert.equal(record.targetKey, "doops-zyj/zyj")
  assert.equal(record.sessionId, "console-abc12345")
  assert.equal(record.summary, "sleep …")
  assert.deepEqual(reloaded.takeInterrupted(scope), [])
})

test("Given a command still running in this page, When residue is taken, Then it is not reported", () => {
  const storage = memoryStorage()
  journal(storage).begin(scope, { ...command, command: "left-over" })
  const commands = journal(storage)
  const live = commands.begin(scope, { ...command, command: "still-running" })
  assert.deepEqual(commands.takeInterrupted(scope).map((record) => record.summary), ["left-over"])
  commands.end(scope, live)
  assert.equal(storage.data.size, 0)
})

test("Given the page is unloading, When the aborted command ends, Then its record is kept", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  const id = commands.begin(scope, command)
  commands.setUnloading(true)
  commands.end(scope, id)
  assert.equal(journal(storage).takeInterrupted(scope).length, 1)
})

test("Given another Gateway or user, When residue is taken, Then records stay with their own scope", () => {
  const storage = memoryStorage()
  journal(storage).begin(scope, command)
  assert.deepEqual(journal(storage).takeInterrupted({ ...scope, username: "ops-li" }), [])
  assert.deepEqual(journal(storage).takeInterrupted({ ...scope, gateway: "https://other.example.test" }), [])
  assert.equal(journal(storage).takeInterrupted(scope).length, 1)
})

test("Given malformed or unavailable storage, When the journal is used, Then it keeps working", () => {
  const storage = memoryStorage()
  const commands = journal(storage)
  commands.begin(scope, command)
  const key = [...storage.data.keys()][0]
  for (const raw of ["not json", "{}", JSON.stringify([{ id: 1 }]), JSON.stringify([{ ...command, id: "x", summary: "s", startedAt: "bad", source: "terminal" }])]) {
    storage.data.set(key, raw)
    assert.deepEqual(journal(storage).takeInterrupted(scope), [], raw)
  }
  const throwing = { getItem() { throw new Error("denied") }, setItem() { throw new Error("denied") }, removeItem() { throw new Error("denied") } }
  const blocked = journal(throwing)
  const id = blocked.begin(scope, command)
  assert.equal(blocked.activeCount(), 1)
  blocked.end(scope, id)
  assert.deepEqual(blocked.takeInterrupted(scope), [])
  assert.deepEqual(new CommandJournal(() => null).takeInterrupted(scope), [])
})

test("Given a command carrying a secret, When it is summarised, Then only the executable name is stored", () => {
  for (const [raw, want] of [
    [`export TOKEN=${CANARY}`, "export …"],
    [`curl -H "Authorization: Bearer ${CANARY}" https://api.example.test`, "curl …"],
    [`mysql -uroot --password=${CANARY} app`, "mysql …"],
    [`mysql -uroot -p${CANARY} app`, "mysql …"],
    [`curl -u admin:${CANARY} https://api.example.test`, "curl …"],
    [`docker login -u bot -p ${CANARY} registry.example.test`, "docker …"],
    [`echo ${CANARY} | sudo -S systemctl restart app`, "echo …"],
    [`DB_PASSWORD='${CANARY}' ./migrate`, "./migrate …"],
    [`git clone https://deploy:${CANARY}@git.example.test/app.git`, "git …"],
    [`kubectl get pods\necho ${CANARY}`, "kubectl …"],
    [`"$(cat /run/secrets/${CANARY})"`, "命令 …"],
    // A token pasted into the terminal by mistake is the first word itself (R5-2).
    [`ghp_${"0a".repeat(18)}`, "命令"],
    [`glpat-${"aB3".repeat(7)}`, "命令"],
    [`${"Zx9".repeat(8)} --flag`, "命令 …"],
    // R6-3: a token is a long run between / . _ - in a word with letters and digits. Cross-compilers and versioned
    // binaries keep their name; a secret key with "/" in it is still a token.
    ["x86_64-linux-gnu-gcc -c main.c", "x86_64-linux-gnu-gcc …"],
    ["aarch64-linux-gnu-objdump -d app", "aarch64-linux-gnu-objdump …"],
    ["x86_64-w64-mingw32-gcc main.c", "x86_64-w64-mingw32-gcc …"],
    ["terraform-provider-aws_v5.31.0", "terraform-provider-aws_v5.31.0"],
    ["node_exporter-1.8.2.linux-amd64 --web.listen-address=:9100", "node_exporter-1.8.2.linux-amd64 …"],
    ["/usr/local/bin/x86_64-linux-gnu-gcc -v", "/usr/local/bin/x86_64-linux-gnu-gcc …"],
    ["/opt/app/releases/2024-11-06/run.sh", "/opt/app/releases/2024-11-06/run.sh"],
    ["scripts/deploy-2024-11.sh", "scripts/deploy-2024-11.sh"],
    ["docker-compose up -d", "docker-compose …"],
    ["k3s kubectl get pods", "k3s …"],
    ["g++ main.cc", "g++ …"],
    ["wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "命令"],
    ["q9Zr2LmX8vTn4KpW7sYb/1HcE5uJ0aFgD3oRiN6x", "命令"],
    [`${"0f1e2d3c".repeat(4)} --flag`, "命令 …"],
    ["eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMifQ.c3ludGhldGljc2lnbmF0dXJlMTIz", "命令"],
    ["python3.11 manage.py", "python3.11 …"],
    ["./bin/server-v2", "./bin/server-v2"],
    ["  df   -h  /  ", "df …"],
    ["uptime", "uptime"],
    ["x".repeat(120), "命令"],
  ]) {
    const summary = commandSummary(raw)
    assert.equal(summary, want, raw)
    assert.ok(!summary.includes(CANARY), `${raw} -> ${summary}`)
  }
})

test("Given a token login without a username, When commands run, Then nothing is stored", () => {
  const storage = memoryStorage()
  const tokenScope = { gateway: scope.gateway }
  const commands = journal(storage)
  const id = commands.begin(tokenScope, command)
  assert.equal(commands.activeCount(), 1, "the unload warning still covers the command")
  assert.equal(storage.data.size, 0)
  commands.end(tokenScope, id)
  assert.deepEqual(journal(storage).takeInterrupted({ gateway: scope.gateway, username: "  " }), [])
})

test("Given a user logs out, When the console state is cleared, Then the next login finds no records", () => {
  const storage = memoryStorage()
  journal(storage).begin(scope, command)
  journal(storage).begin({ ...scope, username: "ops-li" }, command)
  clearConsoleState(storage, scope)
  assert.deepEqual(journal(storage).takeInterrupted(scope), [])
  assert.equal(journal(storage).takeInterrupted({ ...scope, username: "ops-li" }).length, 1, "another user's records stay")
})

test("Given a residual record, When the notice is built, Then it says the outcome is unknown and is not replayed", () => {
  const record = { id: "cmd_1", targetKey: "doops-zyj/zyj", sessionId: "console-abc12345", summary: "sleep 20; echo qa-e1-done", startedAt: NOW.toISOString(), source: "terminal" }
  const same = interruptedCommandNotice(record, { targetKey: "doops-zyj/zyj", sessionId: "console-abc12345" })
  assert.match(same, /^上次命令在页面刷新时被中断，结果未知；请先核对目标实际状态，可在审计页按本会话查询/)
  assert.match(same, /sleep 20; echo qa-e1-done/)
  const moved = interruptedCommandNotice(record, { targetKey: "doops-zyj/test", sessionId: "console-new99999" })
  assert.match(moved, /结果未知/)
  assert.match(moved, /doops-zyj\/zyj/)
  assert.match(moved, /console-abc12345/)
  assert.doesNotMatch(moved, /按本会话查询/)
})

test("Given running commands, When the page is about to unload, Then the warning is shown only while one runs", () => {
  assert.equal(beforeUnloadWarning(0), "")
  assert.equal(beforeUnloadWarning(2), UNLOAD_WARNING)
  assert.equal(UNLOAD_WARNING, "刷新或关闭会中断正在执行的命令")
})
