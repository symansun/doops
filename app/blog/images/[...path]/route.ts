// Serves the shared article images from `blog/images/` under /blog/images/….
//
// Articles reference images as `../images/x.png` relative to their language
// directory, so the rendered Markdown points here instead of at a public/ path.
// Only real image extensions are served: `blog/images` also holds authoring notes
// (for example the figure design doc), and those must never be reachable from
// the public web.

import { readFile, stat } from "node:fs/promises"
import { extname, join, resolve, sep } from "node:path"

// Reads the filesystem on every request: the folder is copied into the image, but
// nothing here is known at build time, so it must not be prerendered.
export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// `blog/images` for the deployed working directory. The folder is copied into the
// runtime image by Dockerfile.web rather than resolved by the bundler, which is
// why the filesystem calls below carry turbopackIgnore hints: without them the
// file tracer cannot prove the subfolder, warns about tracing the whole project,
// and copies every repository file into .next/standalone.
const IMAGE_ROOT = join(process.cwd(), "blog", "images")

const CONTENT_TYPES: Record<string, string> = {
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".webp": "image/webp",
  ".gif": "image/gif",
  ".avif": "image/avif",
  ".svg": "image/svg+xml",
  ".ico": "image/x-icon",
}

// Article image names are semantic, not content-hashed, so a build can replace
// the bytes behind an existing URL. A short max-age plus ETag revalidation keeps
// that honest without paying full body cost on every view.
const CACHE_CONTROL = "public, max-age=300, must-revalidate"

type RouteContext = { params: Promise<{ path: string[] }> }

export async function GET(request: Request, context: RouteContext): Promise<Response> {
  return serve(request, context, true)
}

export async function HEAD(request: Request, context: RouteContext): Promise<Response> {
  return serve(request, context, false)
}

async function serve(request: Request, context: RouteContext, withBody: boolean): Promise<Response> {
  const params = await context.params
  const filePath = resolveImagePath(params.path ?? [])
  if (!filePath) return new Response("Not Found", { status: 404 })

  let info
  try {
    // `filePath` is request data, never build input: keep it out of file tracing.
    info = await stat(/*turbopackIgnore: true*/ filePath)
  } catch {
    return new Response("Not Found", { status: 404 })
  }
  if (!info.isFile()) return new Response("Not Found", { status: 404 })

  const etag = `"${info.size.toString(16)}-${Math.floor(info.mtimeMs).toString(16)}"`
  const headers = new Headers({
    "Content-Type": CONTENT_TYPES[extname(filePath).toLowerCase()] ?? "application/octet-stream",
    "Content-Length": String(info.size),
    "Cache-Control": CACHE_CONTROL,
    ETag: etag,
    "Last-Modified": info.mtime.toUTCString(),
    "X-Content-Type-Options": "nosniff",
    "Content-Disposition": "inline",
  })

  if (matchesEtag(request.headers.get("if-none-match"), etag)) {
    // 304 must not describe a body length.
    headers.delete("Content-Length")
    return new Response(null, { status: 304, headers })
  }
  if (!withBody) return new Response(null, { status: 200, headers })

  try {
    const bytes = await readFile(/*turbopackIgnore: true*/ filePath)
    return new Response(new Uint8Array(bytes), { status: 200, headers })
  } catch {
    return new Response("Not Found", { status: 404 })
  }
}

/**
 * Map catch-all segments to a file inside `blog/images`. Returns null for
 * anything that is not a plain image path: `..`, separators inside a segment,
 * NUL bytes, unknown extensions and paths that escape the root after resolution.
 */
function resolveImagePath(segments: string[]): string | null {
  if (segments.length === 0) return null
  const segmentsToCheck = segments.map((segment) => (segment ?? "").trim())
  for (const segment of segmentsToCheck) {
    if (!segment || segment === "." || segment === "..") return null
    if (segment.includes("/") || segment.includes("\\") || segment.includes("\0")) return null
  }
  const extension = extname(segmentsToCheck[segmentsToCheck.length - 1]).toLowerCase()
  if (!CONTENT_TYPES[extension]) return null

  const target = resolve(IMAGE_ROOT, ...segmentsToCheck)
  if (target === IMAGE_ROOT || !target.startsWith(IMAGE_ROOT + sep)) return null
  return target
}

function matchesEtag(header: string | null, etag: string): boolean {
  if (!header) return false
  return header
    .split(",")
    .map((candidate) => candidate.trim())
    .some((candidate) => candidate === etag || candidate === `W/${etag}` || candidate === "*")
}
