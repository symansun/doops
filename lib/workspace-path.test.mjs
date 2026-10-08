import assert from "node:assert/strict"
import test from "node:test"
import {
  joinWorkspacePath,
  workspaceAbsolutePath,
  workspaceRelativePath,
  workspaceRootPath,
} from "./workspace-path.ts"

test("workspace file paths use relative Agent paths inside the session root", () => {
  assert.equal(workspaceRootPath("session-a"), "/root/ws/session-a")
  assert.equal(
    workspaceRelativePath("session-a", "/root/ws/session-a/src/main.ts"),
    "src/main.ts",
  )
  assert.equal(
    workspaceAbsolutePath("session-a", "src/main.ts"),
    "/root/ws/session-a/src/main.ts",
  )
})

test("workspace paths reject absolute, traversal, sibling, and invalid session paths", () => {
  assert.equal(workspaceRelativePath("session-a", "/root/ws/session-b/secret.txt"), null)
  assert.equal(workspaceRelativePath("session-a", "/root/ws/session-a/../secret.txt"), null)
  assert.equal(workspaceAbsolutePath("session-a", "/etc/passwd"), null)
  assert.equal(workspaceAbsolutePath("session-a", "../secret.txt"), null)
  assert.equal(workspaceRootPath("../session-a"), null)
  assert.equal(workspaceRootPath("session/a"), null)
})

test("new and uploaded files remain under the current workspace directory", () => {
  assert.equal(
    joinWorkspacePath("session-a", "/root/ws/session-a/src", "nested/file.ts"),
    "/root/ws/session-a/src/nested/file.ts",
  )
  assert.equal(
    joinWorkspacePath("session-a", "/root/ws/session-a/src", "../outside.ts"),
    null,
  )
  assert.equal(
    joinWorkspacePath("session-a", "/root/ws/session-a/src", "/tmp/outside.ts"),
    null,
  )
})

test("literal file names are not silently rewritten or interpreted as shell", () => {
  for (const name of ["folder name/file.txt", "prefix$(printf marker)", "quote'file", "a\\b"]) {
    assert.equal(workspaceAbsolutePath("session-a", name), `/root/ws/session-a/${name}`)
    assert.equal(workspaceRelativePath("session-a", `/root/ws/session-a/${name}`), name)
  }
  assert.equal(workspaceAbsolutePath("session-a", " file.txt"), null)
  assert.equal(workspaceAbsolutePath("session-a", "file.txt "), null)
})
