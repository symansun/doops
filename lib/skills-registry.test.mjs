import assert from "node:assert/strict"
import test from "node:test"
import { readdirSync, readFileSync } from "node:fs"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const repoRoot = resolve(here, "..")
const selfPath = resolve(fileURLToPath(import.meta.url))

// SKILL_FIRST_INSPECTION_SPEC.md 第 0 节：巡检能力唯一来源是 Skill 包，
// 严禁在平台核心保留 builtin_strategy / strategy_id 双轨后门。
// 这些标识符已在 SF-21 从 lib/skills-registry.ts 删除；本守卫负责防止复活。
const RETIRED_TOKENS = ["builtin_strategy", "strategy_id", "SKILL_DRIVER_LABELS", "driver_mode"]
const SCAN_DIRS = ["lib", "components"]
const SCAN_EXT = /\.(ts|tsx|mjs|js)$/

function collectFiles(dir) {
  const out = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "node_modules" || entry.name.startsWith(".")) continue
    const full = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...collectFiles(full))
    else if (SCAN_EXT.test(entry.name)) out.push(full)
  }
  return out
}

test("Given frontend sources, When scanned, Then the retired builtin_strategy concept never reappears", () => {
  const hits = []
  let scanned = 0
  for (const dir of SCAN_DIRS) {
    for (const file of collectFiles(join(repoRoot, dir))) {
      // 守卫自身必须列出这些 token，扫描时跳过自己。
      if (resolve(file) === selfPath) continue
      scanned += 1
      readFileSync(file, "utf8")
        .split(/\r?\n/)
        .forEach((line, index) => {
          for (const token of RETIRED_TOKENS) {
            if (line.includes(token)) {
              hits.push(`${file.slice(repoRoot.length + 1)}:${index + 1}: ${token} -> ${line.trim()}`)
            }
          }
        })
    }
  }
  assert.ok(scanned > 100, `扫描覆盖面异常：只扫到 ${scanned} 个文件，守卫必须覆盖 lib/** 与 components/**`)
  assert.deepEqual(hits, [], "退役的 builtin_strategy / strategy_id 双轨概念不得复活：\n" + hits.join("\n"))
})
