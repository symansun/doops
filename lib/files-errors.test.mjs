import test from "node:test"
import assert from "node:assert/strict"

import { FILE_ERROR_RULES, formatBytes, friendlyFileError } from "./files-errors.ts"

test("formatBytes rounds sensibly across units", () => {
  assert.equal(formatBytes(0), "0 B")
  assert.equal(formatBytes(1023), "1023 B")
  assert.equal(formatBytes(1024), "1 KiB")
  assert.equal(formatBytes(4096), "4 KiB")
  assert.equal(formatBytes(256 * 1024), "256 KiB")
  assert.equal(formatBytes(1024 * 1024), "1 MiB")
  assert.equal(formatBytes(1536), "1.5 KiB")
  assert.equal(formatBytes(64 * 1024 * 1024), "64 MiB")
  assert.equal(formatBytes(2 * 1024 * 1024 * 1024), "2 GiB")
})

test("read size limit becomes an actionable message", () => {
  const raw = "doops read is for viewing small text files only; file size 5242880 exceeds limit 1048576"
  assert.equal(
    friendlyFileError(raw),
    "文件 5 MiB 超过可编辑上限 1 MiB：请用行内「下载」或 doops pull 取回后编辑",
  )
})

test("binary and non-regular files are explained", () => {
  assert.match(friendlyFileError("refusing to read: file looks like binary data"), /二进制文件/)
  assert.match(friendlyFileError("doops read is for regular text files only"), /普通文件/)
  assert.match(friendlyFileError("open /root/ws/s/a: is a directory"), /目录/)
})

test("write limit is reported with sizes", () => {
  assert.equal(
    friendlyFileError("file write exceeds max size: 20971520 > 16777216"),
    "内容 20 MiB 超过单次写入上限 16 MiB",
  )
})

test("path, missing, timeout, permission and disk errors are translated", () => {
  assert.equal(friendlyFileError("unsafe workspace path: ../etc/passwd"), "路径必须位于当前工作区内")
  assert.match(friendlyFileError("stat /root/ws/s/x: no such file or directory"), /已不存在/)
  assert.match(friendlyFileError("context deadline exceeded"), /超时/)
  assert.match(friendlyFileError("mkdir /root/ws/s/x: permission denied"), /权限不足/)
  assert.match(friendlyFileError("write /root/ws/s/x: no space left on device"), /磁盘空间不足/)
  assert.match(friendlyFileError("command failed with error: exit status 1"), /exit 1/)
})

test("unknown errors are passed through untouched", () => {
  assert.equal(friendlyFileError("gateway exploded"), "gateway exploded")
  assert.equal(friendlyFileError("   "), "未知错误")
})

test("every rule compiles and renders a non-empty message", () => {
  assert.ok(FILE_ERROR_RULES.length >= 8)
  for (const rule of FILE_ERROR_RULES) {
    assert.ok(rule.pattern instanceof RegExp)
  }
})
