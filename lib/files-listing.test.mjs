import assert from "node:assert/strict"
import test from "node:test"
import { listDirectoryCommand, parseDirectoryListing } from "./files-listing.ts"
import { demoCallTool } from "./demo.ts"

const root = "/root/ws/console-6lacl52e"

async function demoShell(command) {
  let output = ""
  await demoCallTool({ tool: "doops_shell", arguments: { session_id: "console-6lacl52e", command } }, (ev) => {
    if (ev.type === "output") output += ev.data
  })
  return output
}

test("listing the workspace root creates it first so a new session lists as empty", () => {
  assert.equal(listDirectoryCommand(root, root), `mkdir -p '${root}'; ls -1Ap '${root}'`)
  assert.deepEqual(parseDirectoryListing(""), { entries: [], error: "" })
})

test("listing a subdirectory never creates it and reports a missing one as an error", () => {
  const sub = `${root}/releases`
  assert.equal(listDirectoryCommand(root, sub), `ls -1Ap '${sub}'`)
  for (const output of [
    `ls: cannot access '${sub}': No such file or directory\n`,
    `ls: 无法访问 '${sub}': 没有那个文件或目录\n`,
  ]) {
    const parsed = parseDirectoryListing(output)
    assert.deepEqual(parsed.entries, [])
    assert.equal(parsed.error, output.trim())
  }
})

test("listing quotes the path and sorts directories before files", () => {
  assert.equal(listDirectoryCommand(root, `${root}/it's`), `ls -1Ap '${root}/it'\\''s'`)
  assert.deepEqual(parseDirectoryListing("b.txt\nz/\na.md\nc/\n").entries, [
    { name: "c", isDir: true },
    { name: "z", isDir: true },
    { name: "a.md", isDir: false },
    { name: "b.txt", isDir: false },
  ])
})

test("demo mode recognizes the mkdir-then-ls root listing command", async () => {
  const output = await demoShell(listDirectoryCommand(root, root))
  const parsed = parseDirectoryListing(output)
  assert.equal(parsed.error, "")
  assert.ok(parsed.entries.length > 0)
  assert.ok(parsed.entries.some((e) => e.isDir))
})

test("demo mode still reports a missing subdirectory", async () => {
  const output = await demoShell(listDirectoryCommand(root, `${root}/missing`))
  const parsed = parseDirectoryListing(output)
  assert.deepEqual(parsed.entries, [])
  assert.match(parsed.error, /没有那个文件或目录/)
})
