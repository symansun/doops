"use client"

import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react"
import {
  mergeReleases,
  parseInline,
  selectReleases,
  type ChangelogRelease,
  type InlineToken,
  type MergedNotes,
} from "@/lib/changelog"
import { rangeReleases } from "@/lib/whats-new"

// The dialog is presentational: the shell owns when it opens and what the
// browser remembers. Changing the range only re-merges the releases it was
// given, so switching ranges never touches the network.
export type WhatsNewMode = "auto" | "manual"

export interface WhatsNewDialogProps {
  releases: ChangelogRelease[]
  mode: WhatsNewMode
  /** The version the browser recorded last time, captured before this open. */
  sinceVersion: string | null
  /** Whether newer releases existed before this open; picks the default range. */
  hasUnseen: boolean
  onClose: (options: { quietToday: boolean }) => void
}

const COUNT_OPTIONS = [10, 20, 50, 100] as const
type CountOption = (typeof COUNT_OPTIONS)[number]
type RangeChoice = "since" | CountOption

const SINCE_FALLBACK_COUNT: CountOption = 10
const SINCE_EMPTY_NOTE = `上次查看之后没有新版本，下面显示最近 ${SINCE_FALLBACK_COUNT} 个版本的更新。`

interface DialogView {
  notes: MergedNotes | null
  notice: string
}

// "since" is exclusive and can be empty when the recorded version is already
// the latest: fall back to the latest releases and say so, instead of showing
// an empty dialog. "count" is always the latest N releases.
function viewFor(releases: ChangelogRelease[], sinceVersion: string | null, choice: RangeChoice): DialogView {
  if (choice === "since") {
    const newer = sinceVersion
      ? selectReleases(releases, { since: sinceVersion })
      : selectReleases(releases, {})
    if (newer.length > 0) return { notes: mergeReleases(newer), notice: "" }
    return {
      notes: mergeReleases(rangeReleases(releases, { count: SINCE_FALLBACK_COUNT })),
      notice: SINCE_EMPTY_NOTE,
    }
  }
  return { notes: mergeReleases(rangeReleases(releases, { count: choice })), notice: "" }
}

function versionRange(notes: MergedNotes): string {
  return notes.versions === 1 ? "v" + notes.to : "v" + notes.from + " – v" + notes.to
}

// The little Markdown a changelog bullet may carry becomes React nodes; the
// dialog never injects HTML. parseInline already refuses non-http(s) links and
// the check is repeated here so a future call site cannot widen the scheme.
function InlineText({ text }: { text: string }) {
  return (
    <>
      {parseInline(text).map((token: InlineToken, index) => {
        if (token.kind === "code") {
          return (
            <code key={index} className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em] text-foreground">
              {token.text}
            </code>
          )
        }
        if (token.kind === "strong") {
          return (
            <strong key={index} className="font-semibold text-foreground">
              {token.text}
            </strong>
          )
        }
        if (token.kind === "link") {
          if (!/^https?:\/\//i.test(token.href)) return <span key={index}>{token.text}</span>
          return (
            <a
              key={index}
              href={token.href}
              target="_blank"
              rel="noopener noreferrer"
              className="text-primary underline underline-offset-2 hover:opacity-80"
            >
              {token.text}
            </a>
          )
        }
        return <span key={index}>{token.text}</span>
      })}
    </>
  )
}

export function WhatsNewDialog({ releases, mode, sinceVersion, hasUnseen, onClose }: WhatsNewDialogProps) {
  const titleId = useId()
  const closeButton = useRef<HTMLButtonElement>(null)
  // The checkbox only exists inside this open dialog. A ref mirrors it so the
  // Escape/overlay handlers (bound once) can read the latest value.
  const quietTodayRef = useRef(false)
  const [quietToday, setQuietToday] = useState(false)
  // Automatic popups always explain "since last visit"; the manual entry
  // defaults to it only when there is something unseen, otherwise latest 20.
  const [choice, setChoice] = useState<RangeChoice>(() => (mode === "auto" || hasUnseen ? "since" : 20))
  const view = useMemo(() => viewFor(releases, sinceVersion, choice), [releases, sinceVersion, choice])

  const close = useCallback(() => onClose({ quietToday: quietTodayRef.current }), [onClose])

  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = "hidden"
    closeButton.current?.focus()
    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== "Escape") return
      event.preventDefault()
      close()
    }
    document.addEventListener("keydown", onKeyDown, true)
    return () => {
      document.removeEventListener("keydown", onKeyDown, true)
      document.body.style.overflow = previousOverflow
      previous?.focus()
    }
  }, [close])

  const notes = view.notes
  const title = notes ? "v" + notes.to + " 更新内容" : "更新内容"
  const subtitle = notes ? `共 ${notes.versions} 个版本 · ${versionRange(notes)}` : "当前版本没有更新内容"

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-background/70 p-4 backdrop-blur-sm"
      onClick={(event) => {
        if (event.target === event.currentTarget) close()
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        data-testid="whats-new-dialog"
        className="flex max-h-[80vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border bg-card shadow-xl"
      >
        <div className="flex shrink-0 flex-wrap items-start justify-between gap-3 border-b px-5 py-4">
          <div className="min-w-0">
            <h2 id={titleId} className="text-lg font-semibold text-card-foreground">
              {title}
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">{subtitle}</p>
            {view.notice && <p className="mt-1 text-xs text-muted-foreground">{view.notice}</p>}
          </div>
          {mode === "manual" && (
            <label className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
              范围
              <select
                className="input py-1 text-xs"
                value={String(choice)}
                onChange={(event) => {
                  const value = event.target.value
                  const next =
                    value === "since" ? "since" : COUNT_OPTIONS.find((count) => String(count) === value)
                  if (next !== undefined) setChoice(next)
                }}
              >
                <option value="since">自上次查看以来</option>
                {COUNT_OPTIONS.map((count) => (
                  <option key={count} value={count}>
                    最近 {count} 个版本
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {notes ? (
            <div className="space-y-5">
              {notes.sections.map((section) => (
                <section key={section.category}>
                  <h3 className="mb-2 flex items-baseline gap-2 text-sm font-semibold text-foreground">
                    {section.label}
                    <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-normal text-muted-foreground">
                      {section.items.length}
                    </span>
                  </h3>
                  <ul className="space-y-1.5">
                    {section.items.map((item, index) => (
                      <li key={item.version + "-" + index} className="flex items-start gap-2">
                        <span className="mt-0.5 shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-medium leading-none text-muted-foreground">
                          v{item.version}
                        </span>
                        <span className="min-w-0 break-words text-sm leading-relaxed text-foreground">
                          <InlineText text={item.text} />
                        </span>
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </div>
          ) : (
            <p className="py-6 text-center text-sm text-muted-foreground">当前范围内没有可展示的更新内容。</p>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-3 border-t px-5 py-3">
          {mode === "auto" && (
            <label className="flex items-center gap-2 text-xs text-muted-foreground">
              <input
                type="checkbox"
                className="h-3.5 w-3.5 accent-primary"
                checked={quietToday}
                onChange={(event) => {
                  quietTodayRef.current = event.target.checked
                  setQuietToday(event.target.checked)
                }}
              />
              今天不再弹出
            </label>
          )}
          <button
            ref={closeButton}
            onClick={close}
            className="ml-auto rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
          >
            关闭
          </button>
        </div>
      </div>
    </div>
  )
}
