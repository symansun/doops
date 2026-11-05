// The console's top-level views. The header lists the same entries at every
// width: inline beside the target switcher from 640px up, and as a row of its
// own below the header on narrower screens, so none of them needs scrolling.

export type ConsoleView = "console" | "deploy" | "cicd" | "admin"

export interface ConsoleViewEntry {
  id: ConsoleView
  label: string
}

const ENTRIES: ConsoleViewEntry[] = [
  { id: "console", label: "控制台" },
  { id: "deploy", label: "部署" },
  { id: "cicd", label: "CI/CD" },
  { id: "admin", label: "管理" },
]

// The admin entry only decides what is shown; the Gateway still checks every
// admin request.
export function consoleViewEntries(isAdmin: boolean): ConsoleViewEntry[] {
  return ENTRIES.filter((entry) => entry.id !== "admin" || isAdmin)
}
