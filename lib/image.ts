// 镜像构建、复制与 Registry 同步的表单到 doops_shell 参数映射。
// 命令与 CLI backend/cli/cli/doops/image.go、registry_sync.go 生成的完全一致，
// 只在结尾额外打印 manifest digest，便于界面展示 Agent 返回的结果。
// 私有 Registry 凭据只经 Gateway 下发到目标的 authfile，这里从不接收凭据内容。

export const BUILDKIT_SOCKET = "unix:///run/buildkit/buildkitd.sock"
const REGISTRY_TOKEN = /^[A-Za-z0-9._:/-]+$/
const CONTROL_CHARS = /[\u0000-\u001f\u007f]/

export interface ImageBuildInput {
  context: string
  dockerfile: string
  platform: string
  output: "oci" | "image"
  outputPath: string
  image: string
  push: boolean
}

export interface ImageCopyInput {
  source: string
  destination: string
  sourceAuthfile: string
  destinationAuthfile: string
  all: boolean
  verify: boolean
  timeout: number
}

export interface RegistrySyncInput {
  sourceRegistry: string
  targetRegistry: string
  tag: string
  images: string
  sourceAuthfile: string
  targetAuthfile: string
  timeout: number
}

export interface ShellToolRequest {
  tool: "doops_shell"
  arguments: { session_id: string; command: string }
}

export function shellQuote(value: string): string {
  return "'" + value.replaceAll("'", `'"'"'`) + "'"
}

function shellJoin(values: string[]): string {
  return values.map(shellQuote).join(" ")
}

// Go path.Clean 的 POSIX 实现，保证默认 Dockerfile 路径与 CLI 相同。
export function cleanPath(value: string): string {
  if (!value) return "."
  const rooted = value.startsWith("/")
  const out: string[] = []
  for (const part of value.split("/")) {
    if (!part || part === ".") continue
    if (part === "..") {
      if (out.length && out[out.length - 1] !== "..") out.pop()
      else if (!rooted) out.push("..")
      continue
    }
    out.push(part)
  }
  const joined = out.join("/")
  return rooted ? "/" + joined : joined || "."
}

function dirName(value: string): string {
  const clean = cleanPath(value)
  const idx = clean.lastIndexOf("/")
  if (idx < 0) return "."
  return idx === 0 ? "/" : clean.slice(0, idx)
}

function baseName(value: string): string {
  if (!value) return "."
  const trimmed = value.replace(/\/+$/, "")
  if (!trimmed) return "/"
  return trimmed.slice(trimmed.lastIndexOf("/") + 1)
}

function requireText(value: string, message: string): string {
  const trimmed = value.trim()
  if (!trimmed) throw new Error(message)
  if (CONTROL_CHARS.test(trimmed)) throw new Error("参数不能包含换行或控制字符")
  return trimmed
}

function optionalText(value: string): string {
  const trimmed = value.trim()
  if (CONTROL_CHARS.test(trimmed)) throw new Error("参数不能包含换行或控制字符")
  return trimmed
}

function requireTimeout(timeout: number): number {
  if (!Number.isInteger(timeout) || timeout <= 0) throw new Error("超时必须是正整数秒")
  return timeout
}

function shellRequest(sessionId: string, command: string, confirmed: boolean): ShellToolRequest {
  if (!confirmed) throw new Error("请先确认本次镜像变更")
  if (!sessionId.trim()) throw new Error("缺少操作 session")
  return { tool: "doops_shell", arguments: { session_id: sessionId, command } }
}

export function imageBuildCommand(input: ImageBuildInput): string {
  const context = requireText(input.context, "请填写远端构建上下文目录")
  if (input.output !== "oci" && input.output !== "image") throw new Error("输出方式只能是 oci 或 image")
  const outputPath = optionalText(input.outputPath)
  const image = optionalText(input.image)
  const platform = optionalText(input.platform)
  if (input.output === "oci" && !outputPath) throw new Error("输出为 OCI 归档时必须填写归档路径")
  if (input.output === "image" && !image) throw new Error("输出为镜像时必须填写镜像引用")
  if (input.output === "oci" && input.push) throw new Error("只有输出为镜像时才能推送")
  const dockerfilePath = optionalText(input.dockerfile) || cleanPath(context + "/Dockerfile")
  const dockerfileDir = dirName(dockerfilePath)
  const dockerfileName = baseName(dockerfilePath)
  if (dockerfileName === "." || dockerfileName === "/" || dockerfileName === "") throw new Error("Dockerfile 必须指向一个文件")

  let build = `buildctl --addr ${shellQuote(BUILDKIT_SOCKET)} build --progress=plain --frontend dockerfile.v0 --local context=${shellQuote(context)} --local dockerfile=${shellQuote(dockerfileDir)} --opt filename=${shellQuote(dockerfileName)}`
  if (platform) build += " --opt " + shellQuote("platform=" + platform)
  if (input.output === "oci") build += " --output " + shellQuote("type=oci,dest=" + outputPath)
  else build += " --output " + shellQuote(`type=image,name=${image},push=${input.push ? "true" : "false"}`)
  return ["set -eu", "command -v buildctl >/dev/null 2>&1", "test -S /run/buildkit/buildkitd.sock", build].join("\n")
}

export function imageCopyCommand(input: ImageCopyInput): string {
  const source = requireText(input.source, "请填写源镜像引用（含传输方式）")
  const destination = requireText(input.destination, "请填写目标镜像引用（含传输方式）")
  const sourceAuthfile = optionalText(input.sourceAuthfile)
  const destinationAuthfile = optionalText(input.destinationAuthfile)
  const timeout = requireTimeout(input.timeout)
  const parts = ["set -eu", "command -v timeout >/dev/null 2>&1", "command -v skopeo >/dev/null 2>&1"]
  if (input.verify) parts.push("command -v sha256sum >/dev/null 2>&1")
  const copyArgs = ["skopeo", "copy"]
  if (input.all) copyArgs.push("--all")
  copyArgs.push("--retry-times", "3")
  if (sourceAuthfile) copyArgs.push("--src-authfile", sourceAuthfile)
  if (destinationAuthfile) copyArgs.push("--dest-authfile", destinationAuthfile)
  copyArgs.push(source, destination)
  parts.push(`timeout ${timeout}s ${shellJoin(copyArgs)}`)
  if (input.verify) {
    const srcInspect = ["skopeo", "inspect", "--raw", ...(sourceAuthfile ? ["--authfile", sourceAuthfile] : []), source]
    const dstInspect = ["skopeo", "inspect", "--raw", ...(destinationAuthfile ? ["--authfile", destinationAuthfile] : []), destination]
    parts.push(
      `src_digest=$(timeout ${timeout}s ${shellJoin(srcInspect)} | sha256sum | cut -d' ' -f1)`,
      `dst_digest=$(timeout ${timeout}s ${shellJoin(dstInspect)} | sha256sum | cut -d' ' -f1)`,
      `test -n "\${src_digest}" && test "\${src_digest}" = "\${dst_digest}" || { echo 'image manifest digest mismatch' >&2; exit 1; }`,
      `echo "manifest_digest=sha256:\${dst_digest}"`,
    )
  }
  return parts.join("\n")
}

export function registryImages(images: string): string[] {
  return images.split(/[\s,]+/).filter(Boolean)
}

export function registrySyncCommand(input: RegistrySyncInput): string {
  const sourceRegistry = input.sourceRegistry.trim()
  const targetRegistry = input.targetRegistry.trim()
  const tag = input.tag.trim()
  const images = registryImages(input.images)
  if (!sourceRegistry || !targetRegistry || !tag || images.length === 0) {
    throw new Error("请填写源 Registry、目标 Registry、tag 和至少一个镜像名")
  }
  const timeout = requireTimeout(input.timeout)
  for (const [label, value] of [["源 Registry", sourceRegistry], ["目标 Registry", targetRegistry], ["tag", tag]] as const) {
    if (!REGISTRY_TOKEN.test(value)) throw new Error(`${label} 包含不支持的字符`)
  }
  for (const image of images) {
    if (!REGISTRY_TOKEN.test(image)) throw new Error(`镜像名包含不支持的字符：${image}`)
  }
  const sourceAuthfile = optionalText(input.sourceAuthfile)
  const targetAuthfile = optionalText(input.targetAuthfile)
  const srcAuthArgs = sourceAuthfile ? ["--src-authfile", shellQuote(sourceAuthfile)] : []
  const dstAuthArgs = targetAuthfile ? ["--dest-authfile", shellQuote(targetAuthfile)] : []
  const srcInspectArgs = sourceAuthfile ? ["--authfile", shellQuote(sourceAuthfile)] : []
  const dstInspectArgs = targetAuthfile ? ["--authfile", shellQuote(targetAuthfile)] : []
  const parts = ["set -eu", "command -v timeout >/dev/null 2>&1", "command -v sha256sum >/dev/null 2>&1", "command -v skopeo >/dev/null 2>&1"]
  for (const image of images) {
    const sourceRef = `${sourceRegistry}/${image}:${tag}`
    const targetRef = `${targetRegistry}/${image}:${tag}`
    const copyArgs = ["skopeo", "copy", "--all", "--retry-times", "3", ...srcAuthArgs, ...dstAuthArgs, shellQuote("docker://" + sourceRef), shellQuote("docker://" + targetRef)]
    parts.push(`timeout ${timeout}s ${copyArgs.join(" ")}`)
    const srcInspect = ["skopeo", "inspect", "--raw", ...srcInspectArgs, shellQuote("docker://" + sourceRef)]
    const dstInspect = ["skopeo", "inspect", "--raw", ...dstInspectArgs, shellQuote("docker://" + targetRef)]
    parts.push(
      `src_digest=$(timeout ${timeout}s ${srcInspect.join(" ")} | sha256sum | cut -d' ' -f1)`,
      `dst_digest=$(timeout ${timeout}s ${dstInspect.join(" ")} | sha256sum | cut -d' ' -f1)`,
      `test -n "\${src_digest}" && test "\${src_digest}" = "\${dst_digest}" || { echo 'registry manifest digest mismatch for ${image}' >&2; exit 1; }`,
      `echo "${image}:${tag} manifest_digest=sha256:\${dst_digest}"`,
    )
  }
  return parts.join("\n")
}

export function imageBuildRequest(sessionId: string, input: ImageBuildInput, confirmed: boolean): ShellToolRequest {
  return shellRequest(sessionId, imageBuildCommand(input), confirmed)
}

export function imageCopyRequest(sessionId: string, input: ImageCopyInput, confirmed: boolean): ShellToolRequest {
  return shellRequest(sessionId, imageCopyCommand(input), confirmed)
}

export function registrySyncRequest(sessionId: string, input: RegistrySyncInput, confirmed: boolean): ShellToolRequest {
  return shellRequest(sessionId, registrySyncCommand(input), confirmed)
}

// 从 buildctl / skopeo 输出里提取 sha256 digest，去重后按出现顺序返回。
export function extractDigests(text: string): string[] {
  return [...new Set(text.match(/sha256:[a-f0-9]{64}/g) || [])]
}
