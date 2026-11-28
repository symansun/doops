import assert from "node:assert/strict"
import { existsSync, readFileSync } from "node:fs"
import { registerHooks } from "node:module"
import test from "node:test"
import { fileURLToPath } from "node:url"

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith(".") && !specifier.endsWith(".ts") && !specifier.endsWith(".mjs") && !specifier.endsWith(".js") && context.parentURL) {
      const candidate = new URL(`${specifier}.ts`, context.parentURL)
      if (existsSync(fileURLToPath(candidate))) {
        return { url: candidate.href, shortCircuit: true }
      }
    }
    return nextResolve(specifier, context)
  },
})

const {
  isInternalWorkspaceArtifactPath,
  shouldLoadWorkspaceArtifacts,
  shouldSuppressWorkspaceError,
  workspaceArtifactsFromChanges,
} = await import("./agent-code-workspace.ts")

test("isInternalWorkspaceArtifactPath filters DoOps internal target manifest and agent session files only", () => {
  assert.equal(isInternalWorkspaceArtifactPath("target.json"), true)
  assert.equal(isInternalWorkspaceArtifactPath("./target.json"), true)
  assert.equal(isInternalWorkspaceArtifactPath(".target-0123456789abcdef.tmp"), true)
  assert.equal(isInternalWorkspaceArtifactPath("./.target-deadbeef00112233.tmp"), true)
  assert.equal(isInternalWorkspaceArtifactPath(".agent"), true)
  assert.equal(isInternalWorkspaceArtifactPath(".agent/sessions/console-1/messages.json"), true)
  assert.equal(isInternalWorkspaceArtifactPath("./.agent/uploads/note.txt"), true)

  assert.equal(isInternalWorkspaceArtifactPath("report.md"), false)
  assert.equal(isInternalWorkspaceArtifactPath("deploy/values.yaml"), false)
  assert.equal(isInternalWorkspaceArtifactPath("subdir/target.json"), false)
  assert.equal(isInternalWorkspaceArtifactPath("my-target.json"), false)
})

test("workspaceArtifactsFromChanges excludes internal target and .agent files while keeping real deliverables", () => {
  const items = workspaceArtifactsFromChanges("workspace:cluster/node/session-1", {
    gitAvailable: false,
    gitStatus: "no_repository",
    changes: [
      { name: "target.json", path: "target.json", status: "created" },
      { name: ".target-a1b2c3d4e5f60718.tmp", path: ".target-a1b2c3d4e5f60718.tmp", status: "created" },
      { name: "messages.json", path: ".agent/sessions/console-1/messages.json", status: "created" },
      { name: "report.md", path: "report.md", status: "created" },
      { name: "summary.json", path: "out/summary.json", status: "created" },
    ],
  })
  assert.deepEqual(
    items.map((item) => item.artifactId),
    ["workspace:report.md", "workspace:out/summary.json"],
  )
})

test("AgentUiPanel does not load or show workspace deliverables before a conversation starts", () => {
  const source = readFileSync(new URL("../components/agent-ui-panel.tsx", import.meta.url), "utf8")
  assert.match(source, /const hasConversation = snapshot\.items\.length > 0/)
  assert.match(source, /if \(!hasConversation\)\s*\{[\s\S]*?setWorkspaceArtifacts\(\[\]\)[\s\S]*?return[\s\S]*?\}/)
  assert.match(source, /shouldLoadWorkspaceArtifacts\(\{\s*active,\s*hasConversation,\s*connection:\s*snapshot\.connection\s*\}\)/)
  assert.match(source, /snapshot\.connection/)
  assert.match(source, /workspaceReloadTick/)
})

test("AgentUiPanel and WorkloadDeployPanel disable code mode while keeping work mode active", () => {
  for (const rel of ["../components/agent-ui-panel.tsx", "../components/workload-deploy-panel.tsx"]) {
    const source = readFileSync(new URL(rel, import.meta.url), "utf8")
    const workspaceStart = source.indexOf("<AgentWorkspace")
    assert.notEqual(workspaceStart, -1, `${rel} should render <AgentWorkspace>`)
    const afterWorkspace = source.slice(workspaceStart)
    assert.match(afterWorkspace, /allowedModes=\{\[\.\.\.AGENT_WORKSPACE_ALLOWED_MODES\]\}/, `${rel} must restrict AgentWorkspace to work mode`)
    assert.match(afterWorkspace, /defaultMode="work"/, `${rel} must default to work mode`)
    assert.doesNotMatch(afterWorkspace, /\bcodeWorkspace=/, `${rel} must not pass codeWorkspace to AgentWorkspace`)
  }
})

test("shouldLoadWorkspaceArtifacts only loads when active, conversing, and connected", () => {
  assert.equal(shouldLoadWorkspaceArtifacts({ active: true, hasConversation: true, connection: "connected" }), true)
  assert.equal(shouldLoadWorkspaceArtifacts({ active: false, hasConversation: true, connection: "connected" }), false)
  assert.equal(shouldLoadWorkspaceArtifacts({ active: true, hasConversation: false, connection: "connected" }), false)
  assert.equal(shouldLoadWorkspaceArtifacts({ active: true, hasConversation: true, connection: "connecting" }), false)
  assert.equal(shouldLoadWorkspaceArtifacts({ active: true, hasConversation: true, connection: "reconnecting" }), false)
  assert.equal(shouldLoadWorkspaceArtifacts({ active: true, hasConversation: true, connection: "disconnected" }), false)
})

test("shouldSuppressWorkspaceError suppresses AbortError and transient errors during running turns", () => {
  const abortErr = new DOMException("The operation was aborted.", "AbortError")
  assert.equal(shouldSuppressWorkspaceError({ status: "idle", error: abortErr }), true)
  assert.equal(shouldSuppressWorkspaceError({ status: "running", error: new Error("HTTP 502") }), true)
  assert.equal(shouldSuppressWorkspaceError({ status: "idle", error: new Error("工作区读取失败") }), false)
  assert.equal(shouldSuppressWorkspaceError({ status: "completed", error: new Error("工作区读取失败") }), false)
})
