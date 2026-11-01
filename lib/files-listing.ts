export interface ListingEntry {
  name: string
  isDir: boolean
}

// shell 安全转义（单引号包裹）
export function shellQuote(s: string) {
  return "'" + s.replace(/'/g, "'\\''") + "'"
}

// 新会话的工作区根目录尚未创建，列举前先 mkdir -p；子目录不存在时如实报错，不静默创建。
export function listDirectoryCommand(workspaceRoot: string, remoteDir: string) {
  const dir = shellQuote(remoteDir)
  return remoteDir === workspaceRoot ? `mkdir -p ${dir}; ls -1Ap ${dir}` : `ls -1Ap ${dir}`
}

export function parseDirectoryListing(output: string): { entries: ListingEntry[]; error: string } {
  const lines = output.split("\n").filter((l) => l.length > 0)
  if (lines.some((l) => l.includes("No such file") || l.includes("没有那个文件"))) {
    return { entries: [], error: lines.join(" ") }
  }
  const entries = lines.map((l) => ({ name: l.replace(/\/$/, ""), isDir: l.endsWith("/") }))
  entries.sort((a, b) => (a.isDir === b.isDir ? a.name.localeCompare(b.name) : a.isDir ? -1 : 1))
  return { entries, error: "" }
}
