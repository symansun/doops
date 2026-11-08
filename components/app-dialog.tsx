"use client"

import { useCallback, useEffect, useId, useRef, useState } from "react"
import { createPortal } from "react-dom"

export type ConfirmOptions = {
  title: string
  message: string
  confirmLabel?: string
  cancelLabel?: string
  tone?: "default" | "danger"
}

export type PromptOptions = {
  title: string
  message?: string
  label?: string
  placeholder?: string
  defaultValue?: string
  confirmLabel?: string
  cancelLabel?: string
  inputType?: "text" | "password"
}

type ConfirmRequest = {
  id: number
  kind: "confirm"
  options: ConfirmOptions
  resolve: (value: boolean) => void
  settled: boolean
}

type PromptRequest = {
  id: number
  kind: "prompt"
  options: PromptOptions
  resolve: (value: string | null) => void
  settled: boolean
}

type DialogRequest = ConfirmRequest | PromptRequest

function settle(request: DialogRequest, value: boolean | string | null) {
  if (request.settled) return
  request.settled = true
  if (request.kind === "confirm") request.resolve(value === true)
  else request.resolve(typeof value === "string" ? value : null)
}

export function useAppDialog() {
  const seq = useRef(0)
  const requestRef = useRef<DialogRequest | null>(null)
  const [request, setRequest] = useState<DialogRequest | null>(null)

  const replace = useCallback((next: DialogRequest) => {
    const previous = requestRef.current
    if (previous) settle(previous, null)
    requestRef.current = next
    setRequest(next)
  }, [])

  const confirm = useCallback(
    (options: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        replace({ id: ++seq.current, kind: "confirm", options, resolve, settled: false })
      }),
    [replace],
  )

  const prompt = useCallback(
    (options: PromptOptions) =>
      new Promise<string | null>((resolve) => {
        replace({ id: ++seq.current, kind: "prompt", options, resolve, settled: false })
      }),
    [replace],
  )

  const close = useCallback((value: boolean | string | null) => {
    const current = requestRef.current
    if (!current) return
    requestRef.current = null
    setRequest(null)
    settle(current, value)
  }, [])

  useEffect(() => {
    return () => {
      const current = requestRef.current
      if (!current) return
      requestRef.current = null
      settle(current, null)
    }
  }, [])

  const dialog = request ? <AppDialog key={request.id} request={request} onClose={close} /> : null
  return { confirm, prompt, dialog }
}

function AppDialog({
  request,
  onClose,
}: {
  request: DialogRequest
  onClose: (value: boolean | string | null) => void
}) {
  const titleId = useId()
  const messageId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const confirmRef = useRef<HTMLButtonElement>(null)
  const [value, setValue] = useState(request.kind === "prompt" ? (request.options.defaultValue ?? "") : "")
  const [error, setError] = useState("")
  const danger = request.kind === "confirm" && request.options.tone === "danger"
  const message = request.options.message

  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const focusTarget =
      request.kind === "prompt" ? inputRef.current : danger ? cancelRef.current : confirmRef.current
    focusTarget?.focus()
    if (focusTarget instanceof HTMLInputElement) focusTarget.select()
    return () => previous?.focus()
  }, [danger, request.kind])

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (event.key !== "Escape") return
      event.preventDefault()
      event.stopPropagation()
      onClose(request.kind === "confirm" ? false : null)
    }
    document.addEventListener("keydown", onKey, true)
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = "hidden"
    return () => {
      document.removeEventListener("keydown", onKey, true)
      document.body.style.overflow = previousOverflow
    }
  }, [onClose, request.kind])

  function submitPrompt() {
    if (request.kind !== "prompt") return
    const next = request.options.inputType === "password" ? value : value.trim()
    if (!next) {
      setError(request.options.inputType === "password" ? "请输入密码" : "请填写内容")
      inputRef.current?.focus()
      return
    }
    onClose(next)
  }

  function cancel() {
    onClose(request.kind === "confirm" ? false : null)
  }

  if (typeof document === "undefined") return null

  return createPortal(
    <div
      className="fixed inset-0 z-[80] flex items-center justify-center bg-black/60 p-4"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) cancel()
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={message ? messageId : undefined}
        className="w-full max-w-md rounded-lg border bg-card shadow-xl"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="border-b px-4 py-3">
          <h3 id={titleId} className="text-sm font-semibold text-foreground">
            {request.options.title}
          </h3>
        </div>
        <form
          className="space-y-3 p-4"
          onSubmit={(event) => {
            event.preventDefault()
            if (request.kind === "prompt") submitPrompt()
            else onClose(true)
          }}
        >
          {message ? (
            <p id={messageId} className="text-sm leading-relaxed text-foreground">
              {message}
            </p>
          ) : null}
          {request.kind === "prompt" ? (
            <label className="flex flex-col gap-1.5 text-xs text-muted-foreground">
              {request.options.label || "内容"}
              <input
                ref={inputRef}
                value={value}
                onChange={(event) => {
                  setValue(event.target.value)
                  if (error) setError("")
                }}
                placeholder={request.options.placeholder}
                type={request.options.inputType === "password" ? "password" : "text"}
                autoComplete={request.options.inputType === "password" ? "new-password" : "off"}
                spellCheck={false}
                aria-invalid={error ? true : undefined}
                className="input w-full"
              />
            </label>
          ) : null}
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
          <div className="flex justify-end gap-2 pt-1">
            <button
              ref={cancelRef}
              type="button"
              onClick={cancel}
              className="rounded-md border px-3 py-2 text-xs text-foreground transition-colors hover:bg-muted"
            >
              {request.options.cancelLabel || "取消"}
            </button>
            <button
              ref={confirmRef}
              type="submit"
              className={`rounded-md px-3 py-2 text-xs font-medium transition-opacity hover:opacity-90 ${
                danger ? "bg-destructive text-destructive-foreground" : "bg-primary text-primary-foreground"
              }`}
            >
              {request.options.confirmLabel || "确认"}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body,
  )
}
