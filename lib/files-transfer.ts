// 文件面板的大文件/二进制分块传输：file_write 写 base64 分片，doops_shell 组装并校验。

export const MAX_TRANSFER_BYTES = 64 * 1024 * 1024
export const TRANSFER_CHUNK_BYTES = 3 * 1024 * 1024
export const UPLOAD_PARTS_DIR = ".doops-upload"
export const UPLOAD_RECEIPT_PREFIX = "DOOPS_UPLOAD_OK"
export const B64_BEGIN = "__DOOPS_B64_BEGIN__"
export const B64_END = "__DOOPS_B64_END__"
export const STAT_BEGIN = "__DOOPS_STAT_BEGIN__"
export const STAT_END = "__DOOPS_STAT_END__"

// shell 安全转义（单引号包裹）
export function shq(s: string) {
  return "'" + s.replace(/'/g, "'\\''") + "'"
}

// 分片数：空文件也写 1 个空分片，保证远端组装命令不需要特判。
export function planUploadParts(byteLength: number): number {
  if (!Number.isSafeInteger(byteLength) || byteLength < 0) throw new Error("无效的文件大小")
  if (byteLength > MAX_TRANSFER_BYTES) throw new Error("文件超过 64 MiB 上限，请用 doops push 上传")
  return Math.max(1, Math.ceil(byteLength / TRANSFER_CHUNK_BYTES))
}

export function partFileName(index: number): string {
  return `${String(index).padStart(5, "0")}.b64`
}

// TRANSFER_CHUNK_BYTES 是 3 的倍数，每个分片的 base64 可独立解码后顺序拼接。
export function encodeBase64Chunk(bytes: Uint8Array): string {
  let raw = ""
  for (let i = 0; i < bytes.length; i += 0x8000) {
    raw += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  }
  return btoa(raw)
}

export function decodeBase64(text: string): Uint8Array {
  const raw = atob(text.replace(/\s+/g, ""))
  const bytes = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i)
  return bytes
}

export async function sha256Hex(buffer: ArrayBuffer | Uint8Array): Promise<string> {
  const data = buffer instanceof Uint8Array ? buffer : new Uint8Array(buffer)
  const digest = await globalThis.crypto.subtle.digest("SHA-256", data as BufferSource)
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("")
}

export function randomUploadId(): string {
  const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(12))
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("")
}

// 一条命令完成组装、SHA-256 校验、原子改名和分片清理；任何失败都靠 trap 清掉临时目录。
export function assembleUploadCommand(opts: { root: string; partsDir: string; target: string; sha256: string }): string {
  const parts = shq(opts.partsDir)
  const target = shq(opts.target)
  const tmp = `${opts.target}.doops-tmp`
  const tmpQ = shq(tmp)
  return [
    "set -e",
    `mkdir -p -- "$(dirname -- ${target})"`,
    `cleanup() { rm -rf -- ${parts}; rm -f -- ${tmpQ}; rmdir -- "$(dirname -- ${parts})" 2>/dev/null || true; }`,
    "trap cleanup EXIT",
    `cat ${parts}/*.b64 | base64 -d > ${tmpQ}`,
    `actual=$(sha256sum ${tmpQ} | cut -d' ' -f1)`,
    `if [ "$actual" != "${opts.sha256}" ]; then echo "sha256 mismatch"; exit 1; fi`,
    `mv -f -- ${tmpQ} ${target}`,
    `echo "${UPLOAD_RECEIPT_PREFIX} ${opts.sha256}"`,
  ].join("\n")
}

export function parseUploadReceipt(output: string, sha256: string): { ok: boolean; error: string } {
  if (output.includes(`${UPLOAD_RECEIPT_PREFIX} ${sha256}`)) return { ok: true, error: "" }
  if (output.includes("sha256 mismatch")) return { ok: false, error: "远端 SHA-256 校验失败，已清理临时文件" }
  return { ok: false, error: "上传未收到完成回执" }
}

export function assembleStatCommand(abs: string): string {
  const p = shq(abs)
  return `echo ${STAT_BEGIN}; stat -c %s ${p}; sha256sum ${p} | cut -d' ' -f1; echo ${STAT_END}`
}

export function assembleReadChunkCommand(abs: string, offset: number, length: number): string {
  const p = shq(abs)
  return `echo ${B64_BEGIN}; tail -c +${offset + 1} ${p} | head -c ${length} | base64 -w0; echo; echo ${B64_END}`
}

// 从 shell 输出中提取哨兵行之间的内容；找不到标记时返回 null。
export function parseSentinelBlock(output: string, begin: string, end: string): string | null {
  const start = output.indexOf(begin)
  if (start < 0) return null
  const rest = output.slice(start + begin.length)
  const stop = rest.indexOf(end)
  if (stop < 0) return null
  return rest.slice(0, stop).trim()
}

export function parseStatBlock(output: string): { size: number; sha256: string } | null {
  const block = parseSentinelBlock(output, STAT_BEGIN, STAT_END)
  if (block === null) return null
  const lines = block.split("\n").map((l) => l.trim()).filter(Boolean)
  if (lines.length < 2) return null
  const size = Number(lines[0])
  if (!Number.isSafeInteger(size) || size < 0) return null
  const sha256 = lines[1].toLowerCase()
  if (!/^[0-9a-f]{64}$/.test(sha256)) return null
  return { size, sha256 }
}
