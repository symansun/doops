"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { Button, Input, Modal } from "./ui"

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

type PromptRequest = {
  id: number
  options: PromptOptions
  resolve: (value: string | null) => void
  settled: boolean
}

function settle(request: PromptRequest, value: string | null) {
  if (request.settled) return
  request.settled = true
  request.resolve(value)
}

export function useAppDialog() {
  const seq = useRef(0)
  const requestRef = useRef<PromptRequest | null>(null)
  const [request, setRequest] = useState<PromptRequest | null>(null)

  const replace = useCallback((next: PromptRequest) => {
    const previous = requestRef.current
    if (previous) settle(previous, null)
    requestRef.current = next
    setRequest(next)
  }, [])

  const prompt = useCallback(
    (options: PromptOptions) =>
      new Promise<string | null>((resolve) => {
        replace({ id: ++seq.current, options, resolve, settled: false })
      }),
    [replace],
  )

  const close = useCallback((value: string | null) => {
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
  return { prompt, dialog }
}

function AppDialog({
  request,
  onClose,
}: {
  request: PromptRequest
  onClose: (value: string | null) => void
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [value, setValue] = useState(request.options.defaultValue ?? "")
  const [error, setError] = useState("")
  const message = request.options.message

  function submitPrompt() {
    const next = request.options.inputType === "password" ? value : value.trim()
    if (!next) {
      setError(request.options.inputType === "password" ? "请输入密码" : "请填写内容")
      inputRef.current?.focus()
      return
    }
    onClose(next)
  }

  function cancel() {
    onClose(null)
  }

  return (
    <Modal
      title={request.options.title}
      size="sm"
      hideCloseButton
      onClose={cancel}
      onOpenAutoFocus={(event) => {
        event.preventDefault()
        inputRef.current?.focus()
        inputRef.current?.select()
      }}
    >
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault()
          submitPrompt()
        }}
      >
        {message ? (
          <p className="text-sm leading-relaxed text-foreground">
            {message}
          </p>
        ) : null}
        <label className="flex flex-col gap-1.5 text-xs text-muted-foreground">
          {request.options.label || "内容"}
          <Input
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
            className="w-full"
          />
        </label>
        {error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
        <div className="flex justify-end gap-2 pt-1">
          <Button type="button" onClick={cancel} size="sm">
            {request.options.cancelLabel || "取消"}
          </Button>
          <Button type="submit" size="sm" variant="primary">
            {request.options.confirmLabel || "确认"}
          </Button>
        </div>
      </form>
    </Modal>
  )
}
