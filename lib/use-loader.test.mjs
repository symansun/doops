import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

// useLoader 是 React hook，行为在浏览器里才跑得起来；这里锁住它的取消语义和调用方约束，
// 避免以后又有人把 "if (signal.aborted) return" 手抄回组件里。

const hook = readFileSync(new URL("./use-loader.ts", import.meta.url), "utf8")

test("Given a superseded request, Then abort happens after the live flag flips", () => {
  const cleanup = hook.slice(hook.indexOf("return () => {"))
  const liveIdx = cleanup.indexOf("live = false")
  const abortIdx = cleanup.indexOf("controller.abort()")
  assert.ok(liveIdx > -1 && abortIdx > -1, "清理函数必须同时置空 live 并中断请求")
  assert.ok(liveIdx < abortIdx, "live 要在 abort 之前翻掉，否则排队中的 then 仍会写状态")
})

test("Given a failed read, Then loading always ends and the error is user readable", () => {
  assert.match(hook, /catch \(reason\)/)
  assert.match(hook, /setError\(message\(reason\)\)/)
  assert.match(hook, /finally \{/)
  assert.match(hook, /setLoading\(false\)/)
})

test("Given read is redefined every render, Then it does not by itself re-trigger the load", () => {
  // 依赖表只包含 enabled / revision / deps，read 走 ref。
  assert.match(hook, /readRef\.current = read/)
  assert.match(hook, /\[enabled, revision, \.\.\.deps\]/)
})

test("Given any panel, Then it does not hand-roll the abort guard loop", () => {
  const files = [
    "components/website-inventory.tsx",
    "components/database-inventory.tsx",
    "components/k8s-resource-overview.tsx",
  ]
  for (const file of files) {
    const text = readFileSync(new URL(`../${file}`, import.meta.url), "utf8")
    assert.match(text, /useLoader/, `${file} 应改用 useLoader`)
    assert.doesNotMatch(text, /\?\? *new AbortController|const controller = new AbortController\(\)/)
  }
})

test("给定依赖没变时，useLoader 不会重复请求", () => {
  // deps 由调用方显式给出，useEffect 的依赖数组即为它们加上内部 revision；
  // 只要调用方把内联对象换成稳定的标量（cluster/instance/sessionId），就不会抖动。
  assert.match(hook, /const \[revision, reload\] = useState\(0\)/)
})
