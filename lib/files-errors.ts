// 把 Agent / shell 的英文错误翻成可操作的提示；未命中规则时原样返回，避免丢信息。

// 小于 10 时保留一位小数并去掉多余的 .0，其余四舍五入为整数。
function scaled(value: number): string {
  if (value < 10) return String(Number(value.toFixed(1)))
  return String(Math.round(value))
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "?"
  if (bytes < 1024) return `${bytes} B`
  const kib = bytes / 1024
  if (kib < 1024) return `${scaled(kib)} KiB`
  const mib = kib / 1024
  if (mib < 1024) return `${scaled(mib)} MiB`
  return `${scaled(mib / 1024)} GiB`
}

type Rule = { pattern: RegExp; render: (match: RegExpMatchArray) => string }

export const FILE_ERROR_RULES: Rule[] = [
  {
    // doops read is for viewing small text files only; file size 5242880 exceeds limit 1048576
    pattern: /file size (\d+) exceeds limit (\d+)/i,
    render: (m) => `文件 ${formatBytes(Number(m[1]))} 超过可编辑上限 ${formatBytes(Number(m[2]))}：请用行内「下载」或 doops pull 取回后编辑`,
  },
  {
    pattern: /looks like binary data/i,
    render: () => "这是二进制文件，不能按文本打开；可下载后用本地工具处理",
  },
  {
    pattern: /doops read is for regular text files only/i,
    render: () => "只能打开普通文件（目录、管道和符号链接都不支持）",
  },
  {
    // file write exceeds max size: 20971520 > 16777216
    pattern: /file write exceeds max size: (\d+) > (\d+)/i,
    render: (m) => `内容 ${formatBytes(Number(m[1]))} 超过单次写入上限 ${formatBytes(Number(m[2]))}`,
  },
  {
    pattern: /unsafe workspace path|must stay inside the workspace|outside the workspace/i,
    render: () => "路径必须位于当前工作区内",
  },
  {
    pattern: /no such file or directory|does not exist|not found/i,
    render: () => "远端文件已不存在，可能是目录已刷新，请重新列举后再试",
  },
  {
    pattern: /is a directory/i,
    render: () => "这是一个目录，不能按文件打开",
  },
  {
    pattern: /context deadline exceeded|timed out|timeout/i,
    render: () => "远端响应超时，稍后重试",
  },
  {
    pattern: /permission denied/i,
    render: () => "远端拒绝访问（权限不足）",
  },
  {
    pattern: /no space left on device/i,
    render: () => "远端磁盘空间不足",
  },
  {
    // lib/shell-blocks.ts 把非零退出码渲染成这句话
    pattern: /command failed with (?:error: )?exit status (\d+)/i,
    render: (m) => `远端命令执行失败（exit ${m[1]}）`,
  },
]

export function friendlyFileError(raw: string): string {
  const text = (raw || "").trim()
  if (!text) return "未知错误"
  for (const rule of FILE_ERROR_RULES) {
    const match = text.match(rule.pattern)
    if (match) return rule.render(match)
  }
  return text
}
