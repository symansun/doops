import assert from "node:assert/strict"
import test from "node:test"
import {
  assembleReadChunkCommand,
  assembleStatCommand,
  assembleUploadCommand,
  decodeBase64,
  encodeBase64Chunk,
  MAX_TRANSFER_BYTES,
  parseSentinelBlock,
  parseStatBlock,
  parseUploadReceipt,
  partFileName,
  planUploadParts,
  shq,
  TRANSFER_CHUNK_BYTES,
} from "./files-transfer.ts"

test("Given byte lengths at chunk and transfer boundaries, When planning parts, Then counts and rejection match", () => {
  assert.equal(planUploadParts(0), 1, "empty file still writes one empty part")
  assert.equal(planUploadParts(1), 1)
  assert.equal(planUploadParts(TRANSFER_CHUNK_BYTES), 1)
  assert.equal(planUploadParts(TRANSFER_CHUNK_BYTES + 1), 2)
  assert.equal(planUploadParts(MAX_TRANSFER_BYTES), Math.ceil(MAX_TRANSFER_BYTES / TRANSFER_CHUNK_BYTES))
  assert.throws(() => planUploadParts(MAX_TRANSFER_BYTES + 1), /64 MiB/)
  assert.throws(() => planUploadParts(-1), /无效/)
})

test("Given random bytes, When encoded per chunk and decoded, Then data round-trips", () => {
  const bytes = new Uint8Array(TRANSFER_CHUNK_BYTES + 123)
  for (let i = 0; i < bytes.length; i++) bytes[i] = (i * 31 + 7) & 0xff
  let assembled = ""
  for (let i = 0; i < bytes.length; i += TRANSFER_CHUNK_BYTES) {
    assembled += encodeBase64Chunk(bytes.subarray(i, i + TRANSFER_CHUNK_BYTES))
  }
  assert.deepEqual(decodeBase64(assembled), bytes)
  assert.equal(partFileName(0), "00000.b64")
  assert.equal(partFileName(42), "00042.b64")
})

test("Given paths with spaces and single quotes, When assembling the upload command, Then quoting, sha check and trap cleanup are present", () => {
  const cmd = assembleUploadCommand({
    root: "/root/ws/console-x",
    partsDir: "/root/ws/console-x/.doops-upload/ab12cd34ef56",
    target: "/root/ws/console-x/dir with space/it's.bin",
    sha256: "a".repeat(64),
  })
  assert.match(cmd, /set -e/)
  assert.match(cmd, /trap cleanup EXIT/)
  assert.match(cmd, /rm -rf -- '\/root\/ws\/console-x\/\.doops-upload\/ab12cd34ef56'/)
  assert.match(cmd, /rmdir -- "\$\(dirname -- '\/root\/ws\/console-x\/\.doops-upload\/ab12cd34ef56'\)" 2>\/dev\/null \|\| true/)
  assert.match(cmd, /'\/root\/ws\/console-x\/dir with space\/it'\\''s\.bin\.doops-tmp'/)
  assert.match(cmd, /cat '\/root\/ws\/console-x\/\.doops-upload\/ab12cd34ef56'\/\*\.b64 \| base64 -d/)
  assert.match(cmd, /sha256sum .* \| cut -d' ' -f1/)
  assert.match(cmd, new RegExp(`\\[ "\\$actual" != "${"a".repeat(64)}" \\]`))
  assert.match(cmd, /mv -f -- .*\.doops-tmp' '/)
  assert.match(cmd, new RegExp(`DOOPS_UPLOAD_OK ${"a".repeat(64)}`))
})

test("Given shell outputs, When parsing the upload receipt, Then ok, mismatch and missing sentinel are distinguished", () => {
  const sha = "b".repeat(64)
  assert.deepEqual(parseUploadReceipt(`noise\nDOOPS_UPLOAD_OK ${sha}\n`, sha), { ok: true, error: "" })
  assert.equal(parseUploadReceipt("partial\nsha256 mismatch\n", sha).ok, false)
  assert.match(parseUploadReceipt("partial\nsha256 mismatch\n", sha).error, /SHA-256/)
  assert.equal(parseUploadReceipt("nothing useful\n", sha).ok, false)
  assert.match(parseUploadReceipt("nothing useful\n", sha).error, /回执/)
  assert.equal(parseUploadReceipt(`DOOPS_UPLOAD_OK ${"c".repeat(64)}`, sha).ok, false, "receipt for a different sha does not count")
})

test("Given noisy shell output, When parsing sentinel blocks, Then inner content is returned or null", () => {
  const stat = assembleStatCommand("/root/ws/console-x/f.bin")
  assert.match(stat, /stat -c %s '\/root\/ws\/console-x\/f\.bin'/)
  assert.match(stat, /sha256sum/)
  const output = `\rheartbeat\n__DOOPS_STAT_BEGIN__\n12345\n${"d".repeat(64)}\n__DOOPS_STAT_END__\ntrailing\n`
  assert.deepEqual(parseStatBlock(output), { size: 12345, sha256: "d".repeat(64) })
  assert.equal(parseStatBlock("no markers"), null)
  assert.equal(parseStatBlock(`__DOOPS_STAT_BEGIN__\nabc\n${"d".repeat(64)}\n__DOOPS_STAT_END__`), null)

  const chunk = assembleReadChunkCommand("/root/ws/console-x/f.bin", 3 * TRANSFER_CHUNK_BYTES, TRANSFER_CHUNK_BYTES)
  assert.match(chunk, /tail -c \+9437185 .* \| head -c 3145728 \| base64 -w0/)
  const b64 = parseSentinelBlock(`x\n__DOOPS_B64_BEGIN__\nQUJD\n__DOOPS_B64_END__\ny`, "__DOOPS_B64_BEGIN__", "__DOOPS_B64_END__")
  assert.equal(b64, "QUJD")
  assert.equal(parseSentinelBlock("missing", "__DOOPS_B64_BEGIN__", "__DOOPS_B64_END__"), null)
  assert.deepEqual(decodeBase64(b64), new Uint8Array([65, 66, 67]))
})

test("Given shell quoting inputs, When quoting, Then embedded single quotes are escaped", () => {
  assert.equal(shq("plain"), "'plain'")
  assert.equal(shq("a'b"), "'a'\\''b'")
})
