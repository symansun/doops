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

export type ConfirmOptions = {
  title: string
  message?: string
  confirmLabel?: string
  cancelLabel?: string
  /** 破坏性操作：确认按钮改用 danger 样式。 */
  danger?: boolean
}

type PromptRequest = {
  id: number
  options: PromptOptions
  resolve: (value: string | null) => void
  settled: boolean
}

type ConfirmRequest = {
  id: number
  options: ConfirmOptions
  resolve: (value: boolean) => void
  settled: boolean
}

function settleConfirm(request: ConfirmRequest, value: boolean) {
  if (request.settled) return
  request.settled = true
  request.resolve(value)
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

  const confirmRef = useRef<ConfirmRequest | null>(null)
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(null)

  const confirm = useCallback(
    (options: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        const previous = confirmRef.current
        if (previous) settleConfirm(previous, false)
        const next: ConfirmRequest = { id: ++seq.current, options, resolve, settled: false }
        confirmRef.current = next
        setConfirmRequest(next)
      }),
    [],
  )

  const closeConfirm = useCallback((value: boolean) => {
    const current = confirmRef.current
    if (!current) return
    confirmRef.current = null
    setConfirmRequest(null)
    settleConfirm(current, value)
  }, [])

  useEffect(() => {
    return () => {
      const current = confirmRef.current
      if (!current) return
      confirmRef.current = null
      settleConfirm(current, false)
    }
  }, [])

  const dialog = (
    <>
      {request ? <AppDialog key={`prompt-${request.id}`} request={request} onClose={close} /> : null}
      {confirmRequest ? (
        <ConfirmDialog key={`confirm-${confirmRequest.id}`} request={confirmRequest} onClose={closeConfirm} />
      ) : null}
    </>
  )
  return { prompt, confirm, dialog }
}

function ConfirmDialog({
  request,
  onClose,
}: {
  request: ConfirmRequest
  onClose: (value: boolean) => void
}) {
  return (
    <Modal
      title={request.options.title}
      size="sm"
      hideCloseButton
      onClose={() => onClose(false)}
    >
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault()
          onClose(true)
        }}
      >
        {request.options.message ? (
          <p className="text-sm leading-relaxed text-foreground">{request.options.message}</p>
        ) : null}
        <div className="flex justify-end gap-2 pt-1">
          <Button type="button" onClick={() => onClose(false)} size="sm">
            {request.options.cancelLabel || "取消"}
          </Button>
          <Button type="submit" size="sm" variant={request.options.danger ? "danger" : "primary"}>
            {request.options.confirmLabel || "确认"}
          </Button>
        </div>
      </form>
    </Modal>
  )
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
