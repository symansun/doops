export type MachineKind = "vm" | "physical"
export type TextInventoryMachine = {
  asset_id: string; name: string; address?: string; kind?: MachineKind
  cpu_count?: number | null; memory_gib?: number | null; disk_gib?: number | null; host_asset_id?: string
}
export type TextMachineRow = {
  line: number; id: string; name: string; address: string; group: string; username: string
  internal: string; port: number; existing: boolean
}
export type MachineTextPlan = {
  rows: TextMachineRow[]; issues: { line: number; message: string }[]; csv: string
}
const maxBytes = 1024 * 1024
const encoder = new TextEncoder()
const header = ["asset_id", "name", "kind", "address", "cpu_count", "memory_gib", "disk_gib", "host_asset_id"]
const ipv4 = (value: string) => /^(?:0|[1-9]\d{0,2})(?:\.(?:0|[1-9]\d{0,2})){3}$/.test(value) && value.split(".").every(n => Number(n) <= 255)
const csvLine = (fields: (string | number)[]) => fields.map(v => '"' + String(v).replaceAll('"', '""') + '"').join(",")

/** Browser-local parsing. Only csv may contain passwords. Row diagnostics never include source text. */
export function machineTextPlan(source: string, machines: readonly TextInventoryMachine[], kind: MachineKind, saveLogins: boolean): MachineTextPlan {
  const rows: TextMachineRow[] = [], issues: MachineTextPlan["issues"] = []
  const issue = (line: number, message: string) => issues.push({ line, message })
  if (source.length > maxBytes || encoder.encode(source).length > maxBytes) {
    issue(0, "文本超过 1 MiB，请分批粘贴。")
    return { rows, issues, csv: "" }
  }
  const byIP = new Map<string, TextInventoryMachine[]>(), byId = new Set<string>()
  for (const m of machines) {
    byId.add(m.asset_id)
    if (m.address) byIP.set(m.address, [...(byIP.get(m.address) ?? []), m])
  }
  const records = [csvLine(saveLogins ? [...header, "username", "password"] : header)]
  let group = "", pendingGroupLine = 0
  const lines = source.split(/\r\n|[\r\n]/)
  for (let i = 0; i < lines.length; i++) {
    const line = i + 1, text = lines[i].trim()
    if (!text) continue
    const title = /^(?:([\p{L}][\p{L}\d_-]{0,31})[:：]?|\[([\p{L}\d][\p{L}\d _-]{0,31})\])$/u.exec(text)
    if (title) {
      if (pendingGroupLine) issue(pendingGroupLine, "分组下缺少机器行。")
      group = title[1] ?? title[2]; pendingGroupLine = line
      continue
    }
    pendingGroupLine = 0
    if (rows.length + issues.length >= 2000) { issue(line, "每批最多 2,000 条机器记录。"); break }
    const [address, username = "", password = "", ...tail] = text.split(/\s+/u)
    if (!ipv4(address)) { issue(line, "机器行须以完整 IPv4 地址开头，分组名称单独一行。"); continue }
    if ((username || saveLogins) && (!password || !/^[a-zA-Z_][\w.@-]{0,127}$/.test(username))) {
      issue(line, "请核对登录账号和密码，含空白的密码请使用 CSV。"); continue
    }
    let internal = "", port = 22
    if (tail.length && ipv4(tail[0])) internal = tail.shift()!
    const hasPort = tail.length > 0
    if (tail[0] === "-p") tail.shift()
    if (hasPort && (tail.length !== 1 || !/^\d{1,5}$/.test(tail[0]) || Number(tail[0]) < 1 || Number(tail[0]) > 65535)) {
      issue(line, "可选字段依次为内网 IPv4、SSH 端口（1–65535），端口前可加 -p。"); continue
    }
    if (hasPort) port = Number(tail[0])
    const matches = byIP.get(address) ?? [], matched = matches[0]
    const id = matched?.asset_id ?? 'ip-' + address.replaceAll('.', '-')
    if (matches.length > 1 || (!matched && byId.has(id))) { issue(line, "管理地址或生成的资产编号在台账中存在冲突，请先核对。"); continue }
    const name = matched?.name ?? [group, address, internal && '内网 ' + internal, hasPort && 'SSH ' + port].filter(Boolean).join(' · ')
    const fields = [id, name, matched?.kind ?? kind, address, matched?.cpu_count ?? '', matched?.memory_gib ?? '', matched?.disk_gib ?? '', matched?.host_asset_id ?? '']
    records.push(csvLine(saveLogins ? [...fields, username, password] : fields))
    rows.push({ line, id, name, address, group, username, internal, port, existing: !!matched })
  }
  if (pendingGroupLine) issue(pendingGroupLine, "分组下缺少机器行。")
  const csv = records.join("\r\n")
  if (encoder.encode(csv).length > maxBytes) issue(0, "转换结果超过 1 MiB，请分批导入。")
  return { rows, issues, csv: rows.length && !issues.length ? csv : "" }
}
