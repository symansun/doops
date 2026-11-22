"use client"

import { Check, ChevronDown } from "lucide-react"
import { forwardRef, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type SelectHTMLAttributes } from "react"

export type SelectProps = Omit<SelectHTMLAttributes<HTMLSelectElement>, "multiple" | "size"> & { scale?: "default" | "sm" }
type Choice = { label: string; disabled: boolean }

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select({
  children, className = "", scale, id, title, disabled, required, value, defaultValue, onChange,
  "aria-label": label, "aria-labelledby": labelledBy, "aria-describedby": describedBy,
  "aria-invalid": ariaInvalid, ...fieldProps
}, ref) {
  const uid = useId()
  const trigger = useRef<HTMLButtonElement>(null)
  const field = useRef<HTMLSelectElement>(null)
  const menu = useRef<HTMLSpanElement>(null)
  const search = useRef({ text: "", at: 0 })
  const [open, setOpen] = useState(false)
  const [choices, setChoices] = useState<Choice[]>([])
  const [caption, setCaption] = useState("")
  const [active, setActive] = useState(-1)
  const [invalid, setInvalid] = useState(false)

  function syncCaption() {
    setCaption(field.current?.selectedOptions[0]?.label ?? "")
    if (field.current?.validity.valid) setInvalid(false)
  }
  useLayoutEffect(() => {
    syncCaption()
    if (!open || !field.current) return
    const options = Array.from(field.current.options, opt => ({ label: opt.label, disabled: opt.disabled }))
    setChoices(options)
    setActive(index => options[index] && !options[index].disabled ? index : options.findIndex(opt => !opt.disabled))
  }, [children, value, defaultValue, open])
  useEffect(() => {
    const form = field.current?.form
    const reset = () => queueMicrotask(() => { syncCaption(); setInvalid(false); setOpen(false) })
    form?.addEventListener("reset", reset)
    return () => form?.removeEventListener("reset", reset)
  }, [fieldProps.form])
  useLayoutEffect(() => {
    if (!open || !menu.current || !trigger.current) return
    if (disabled) { setOpen(false); return }
    const popup = menu.current, button = trigger.current
    popup.showPopover?.()
    function position() {
      const rect = button.getBoundingClientRect()
      const below = window.innerHeight - rect.bottom - 8
      const above = rect.top - 8
      const upwards = below < 200 && above > below
      const left = Math.max(4, Math.min(rect.left, window.innerWidth - popup.offsetWidth - 4))
      Object.assign(popup.style, {
        left: left + "px", minWidth: Math.min(rect.width, window.innerWidth - 8) + "px",
        maxHeight: Math.min(320, Math.max(0, upwards ? above : below)) + "px",
        top: upwards ? "auto" : rect.bottom + 4 + "px",
        bottom: upwards ? window.innerHeight - rect.top + 4 + "px" : "auto",
      })
    }
    position()
    const outside = (event: PointerEvent) => {
      if (!event.composedPath().includes(popup) && !event.composedPath().includes(button)) setOpen(false)
    }
    window.addEventListener("resize", position)
    document.addEventListener("scroll", position, true)
    document.addEventListener("pointerdown", outside, true)
    return () => {
      window.removeEventListener("resize", position)
      document.removeEventListener("scroll", position, true)
      document.removeEventListener("pointerdown", outside, true)
    }
  }, [open, disabled, choices])
  useLayoutEffect(() => {
    if (open) menu.current?.children[active]?.scrollIntoView({ block: "nearest" })
  }, [active, open, choices])

  function expand() {
    search.current.text = ""
    setActive(field.current?.selectedIndex ?? -1)
    setOpen(true)
  }
  function choose(index: number) {
    const select = field.current
    if (!select || !select.options[index] || select.options[index].disabled) return
    if (select.selectedIndex !== index) {
      select.selectedIndex = index
      select.dispatchEvent(new Event("change", { bubbles: true }))
      syncCaption()
    }
    setOpen(false)
    trigger.current?.focus({ preventScroll: true })
  }
  function keyDown(event: KeyboardEvent<HTMLButtonElement>) {
    const key = event.key
    if (key === "Tab") { setOpen(false); return }
    if (key === "Escape" && open) {
      event.preventDefault(); event.stopPropagation(); setOpen(false); return
    }
    if (key === "Enter" || key === " ") {
      event.preventDefault()
      if (open) choose(active); else expand()
      return
    }
    const options = Array.from(field.current?.options ?? [])
    const enabled = options.map((opt, index) => opt.disabled ? -1 : index).filter(index => index >= 0)
    if (!enabled.length || event.ctrlKey || event.metaKey || event.altKey) return
    const current = open ? active : field.current?.selectedIndex ?? -1
    let next: number | undefined
    if (key === "Home") next = enabled[0]
    else if (key === "End") next = enabled.at(-1)
    else if (key === "ArrowDown") next = enabled.find(i => i > current) ?? enabled.at(-1)
    else if (key === "ArrowUp") next = enabled.findLast(i => i < current) ?? enabled[0]
    else if (key.length === 1) {
      const now = Date.now()
      search.current.text = (now - search.current.at < 700 ? search.current.text : "") + key.toLocaleLowerCase()
      search.current.at = now
      next = enabled.find(i => options[i].label.toLocaleLowerCase().startsWith(search.current.text))
    } else return
    event.preventDefault()
    if (next !== undefined) { setActive(next); setOpen(true) }
  }

  const buttonId = id ?? uid + "-trigger", listId = uid + "-list"
  return <>
    <button ref={trigger} id={buttonId} type="button" role="combobox" title={title} disabled={disabled}
      aria-label={label} aria-labelledby={labelledBy} aria-describedby={invalid ? uid + "-error" : describedBy}
      aria-expanded={open} aria-haspopup="listbox" aria-controls={open ? listId : undefined}
      aria-activedescendant={open && active >= 0 ? listId + "-" + active : undefined}
      aria-required={required} aria-invalid={invalid || ariaInvalid}
      className={[
        "input inline-flex max-w-full items-center justify-between gap-2 text-left",
        scale === "sm" && "input-sm", className,
      ].filter(Boolean).join(" ")}
      onClick={() => open ? setOpen(false) : expand()} onKeyDown={keyDown} onBlur={() => setOpen(false)}>
      <span className="min-w-0 truncate">{caption || "请选择"}</span>
      <ChevronDown aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
    </button>
    {/* The non-interactive backing field owns FormData, native constraint checks and change events. */}
    <select {...fieldProps} hidden aria-hidden="true" tabIndex={-1} disabled={disabled} required={required}
      value={value} defaultValue={defaultValue} onChange={onChange}
      onInvalid={event => { event.preventDefault(); setInvalid(true); trigger.current?.focus() }}
      ref={node => { field.current = node; if (typeof ref === "function") ref(node); else if (ref) ref.current = node }}>
      {children}
    </select>
    {invalid && <span id={uid + "-error"} role="alert" className="text-sm text-destructive">请选择一项。</span>}
    {open && <span ref={menu} popover="manual" role="listbox" id={listId} aria-labelledby={labelledBy || buttonId}
      className="fixed inset-auto z-[100] m-0 w-max max-w-[calc(100vw-8px)] overflow-y-auto overscroll-contain rounded-lg border border-border bg-popover p-1 text-sm text-popover-foreground shadow-xl"
      onMouseDown={event => event.preventDefault()} onClick={event => event.preventDefault()}>
      {choices.map((opt, i) => <span key={i} id={listId + "-" + i} role="option"
        aria-selected={i === field.current?.selectedIndex} aria-disabled={opt.disabled || undefined}
        className={"flex cursor-pointer items-center justify-between gap-3 rounded px-3 py-2 aria-disabled:cursor-not-allowed aria-disabled:opacity-50 " + (active === i ? "bg-muted" : "")}
        onPointerMove={() => { if (!opt.disabled) setActive(i) }} onClick={event => { event.preventDefault(); choose(i) }}>
        <span className="min-w-0 break-words">{opt.label}</span>
        {i === field.current?.selectedIndex && <Check aria-hidden="true" className="size-4 shrink-0 text-primary" />}
      </span>)}
    </span>}
  </>
})
