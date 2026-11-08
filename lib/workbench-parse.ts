/** kubectl custom-columns: header plus rows split on two or more spaces. `<none>` is empty. */
export function parseAlignedColumns(text: string): string[][] | null {
  const lines = text.split(/\r?\n/).map((line) => line.trimEnd()).filter((line) => line.trim())
  if (lines.length === 0) return null
  const width = lines[0].trim().split(/\s{2,}/).length
  if (width < 2) return null
  const rows: string[][] = []
  for (const line of lines.slice(1)) {
    const cells = line.trim().split(/\s{2,}/)
    if (cells.length !== width) return null
    rows.push(cells.map((cell) => (cell.trim() === "<none>" ? "" : cell.trim())))
  }
  return rows
}

const LABEL_KEY = /^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*\/)?[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$/
const LABEL_VALUE = /^([A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?)?$/
// The doops_k8s selector pattern; a selector it would reject is never sent.
const TOOL_SELECTOR = /^[A-Za-z0-9_./!=(), -]{1,512}$/

/**
 * `jsonpath={.spec.selector}` prints the selector as JSON. It becomes a kubectl
 * label selector only when every term can be written exactly; otherwise null.
 */
export function labelSelectorString(text: string): string | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(text.trim())
  } catch {
    return null
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null
  const { matchLabels, matchExpressions } = parsed as { matchLabels?: unknown; matchExpressions?: unknown }
  const parts: string[] = []
  if (matchLabels !== undefined) {
    if (!matchLabels || typeof matchLabels !== "object" || Array.isArray(matchLabels)) return null
    for (const [key, value] of Object.entries(matchLabels)) {
      if (!LABEL_KEY.test(key) || typeof value !== "string" || !LABEL_VALUE.test(value)) return null
      parts.push(`${key}=${value}`)
    }
  }
  if (matchExpressions !== undefined) {
    if (!Array.isArray(matchExpressions)) return null
    for (const item of matchExpressions) {
      if (!item || typeof item !== "object") return null
      const { key, operator, values = [] } = item as { key?: unknown; operator?: unknown; values?: unknown }
      if (typeof key !== "string" || !LABEL_KEY.test(key) || !Array.isArray(values)) return null
      if (values.some((value) => typeof value !== "string" || !value || !LABEL_VALUE.test(value))) return null
      if (operator === "In" || operator === "NotIn") {
        if (values.length === 0) return null
        parts.push(`${key} ${operator === "In" ? "in" : "notin"} (${values.join(",")})`)
      } else if (operator === "Exists" || operator === "DoesNotExist") {
        if (values.length !== 0) return null
        parts.push(operator === "Exists" ? key : `!${key}`)
      } else {
        return null
      }
    }
  }
  if (parts.length === 0) return null
  const selector = parts.join(",")
  return TOOL_SELECTOR.test(selector) ? selector : null
}

/** `true,false` from containerStatuses[*].ready becomes `1/2`. */
export function formatReadyList(value: string): string {
  if (!value) return ""
  const parts = value.split(",").map((part) => part.trim()).filter(Boolean)
  if (parts.length === 0 || parts.some((part) => part !== "true" && part !== "false")) return value
  return `${parts.filter((part) => part === "true").length}/${parts.length}`
}
