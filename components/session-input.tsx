"use client"

import { useEffect, useState } from "react"
import { workspaceRootPath } from "@/lib/workspace-path"
import { Input } from "./ui"

// The panels are keyed by session id, so a change remounts them and reads the
// new workspace. Commit on Enter or blur instead of on every keystroke.
export function SessionInput({ value, onCommit, disabled, className }: {
  value: string
  onCommit: (next: string) => void
  disabled?: boolean
  className?: string
}) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  const next = draft.trim()
  const invalid = workspaceRootPath(next) === null
  function commit() {
    if (invalid) return setDraft(value)
    setDraft(next)
    if (next !== value) onCommit(next)
  }
  return (
    <Input
      scale="sm"
      value={draft}
      disabled={disabled}
      aria-invalid={invalid || undefined}
      title={invalid ? "session 须以字母或数字开头，只含字母、数字、点、下划线和连字符" : "按 Enter 或移开焦点后切换会话"}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing) return
        if (e.key === "Enter") { e.preventDefault(); if (!invalid) commit() }
        else if (e.key === "Escape") setDraft(value)
      }}
      spellCheck={false}
      className={className}
    />
  )
}
