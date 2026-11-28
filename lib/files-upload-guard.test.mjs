// 上传输入框的选择快照必须在清空 input.value 之前取好。
// Chromium 的 FileList 是活的：把 value 置空后，先前拿到的 fileList 会变成长度 0，
// 于是「点上传按钮 → 选择文件 → 上传结束 0/0」而没有任何报错。这个守卫只锁顺序，不改实现。
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"

const source = readFileSync(new URL("../components/files-panel.tsx", import.meta.url), "utf8")

const sourceIndexOf = (needle) => {
  const index = source.indexOf(needle)
  assert.notEqual(index, -1, `components/files-panel.tsx 缺少预期片段：${needle}`)
  return index
}

test("上传前先复制 FileList，再清空两个 input 的 value", () => {
  const picked = sourceIndexOf("const picked = Array.from(fileList)")
  const clearFile = sourceIndexOf('if (fileInputRef.current) fileInputRef.current.value = ""')
  const clearDir = sourceIndexOf('if (dirInputRef.current) dirInputRef.current.value = ""')
  assert.ok(picked < clearFile, "const picked = Array.from(fileList) 必须在清空 fileInputRef 之前")
  assert.ok(picked < clearDir, "const picked = Array.from(fileList) 必须在清空 dirInputRef 之前")
})

test("上传循环遍历复制后的数组，而不是活的 FileList", () => {
  assert.ok(source.includes("for (const file of picked)"), "上传循环必须遍历 picked 快照")
  assert.ok(
    !/for \(const file of Array\.from\(fileList\)\)/.test(source),
    "不要再从 fileList 现取：清空 value 之后它已经是空的",
  )
})
