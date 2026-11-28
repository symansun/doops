"use client"

import { useEffect, useRef, useState } from "react"
import { createPortal, flushSync } from "react-dom"
import { containsSecret, decodeReply, instruction, readLabelledText, validateSuggestions, type Suggestion } from "@/lib/form-assist"
import { applyOne, fieldsOf, scopeSelector, type Bound } from "@/lib/form-assist-dom"
import { Button, Card, Checkbox, Textarea } from "./ui"

function Panel({ scope }: { scope: HTMLElement }) {
  const [open, show] = useState(false)
  const [source, describe] = useState("")
  const [fields, setFields] = useState<Bound[]>([])
  const [rows, setRows] = useState<Suggestion[]>([])
  const [chosen, choose] = useState<string[]>([])
  const [note, status] = useState("")
  const [busy, working] = useState(false)
  const worker = useRef<Worker | null>(null)
  useEffect(() => () => worker.current?.terminate(), [])
  function stop() {
    worker.current?.terminate()
    worker.current = null
    working(false)
  }
  function close() {
    stop()
    show(false)
    describe("")
    setRows([])
    setFields([])
    choose([])
    status("")
  }
  function preview(list: Suggestion[], f: Bound[]) {
    setFields(f)
    setRows(list)
    choose(
      list
        .filter((r) => {
          const el = f.find((x) => x.id === r.id)?.el
          return el && !el.value
        })
        .map((r) => r.id),
    )
    status(list.length ? "请核对建议。已有值的字段需要手动勾选才会覆盖。" : "没有找到明确的字段值。")
  }
  function generate(model: boolean) {
    stop()
    setRows([])
    choose([])
    const f = fieldsOf(scope)
    setFields(f)
    if (!source.trim() || containsSecret(source)) {
      status("请移除密码、令牌或带认证信息的连接串。")
      return
    }
    if (!f.length) {
      status("没有可辅助填写的字段。")
      return
    }
    if (!model) {
      preview(readLabelledText(source, f), f)
      return
    }
    if (!("gpu" in navigator)) {
      status("浏览器不支持 WebGPU，可用键值解析或手工填写。")
      return
    }
    try {
      const w = new Worker(new URL("../lib/form-assist.worker.ts", import.meta.url), { type: "module" })
      worker.current = w
      working(true)
      status("正在启动本地模型，可随时取消。")
      w.onerror = () => {
        if (worker.current !== w) return
        stop()
        status("本地模型启动失败，请检查 WebGPU、网络及可用内存。")
      }
      w.onmessage = ({ data }) => {
        if (worker.current !== w) return
        if (typeof data.progress === "number") {
          status("准备本地模型 " + data.progress + "%")
          return
        }
        stop()
        try {
          if (data.error) throw Error(data.error)
          preview(validateSuggestions(decodeReply(data.text), f, source), f)
        } catch (e) {
          status((e as Error).message)
        }
      }
      w.postMessage(instruction(f, source))
    } catch {
      stop()
      status("无法启动本地推理线程。")
    }
  }
  function apply() {
    let count = 0
    const initial = fieldsOf(scope)
    const allowed = fields.filter((f) => initial.some((x) => x.el === f.el && x.label === f.label && x.original === f.original))
    for (const r of rows) {
      if (!chosen.includes(r.id)) continue
      const f = allowed.find((x) => x.id === r.id)
      if (!f) continue
      const now = fieldsOf(scope).find((x) => x.el === f.el && x.label === f.label)
      if (!now || !validateSuggestions([{ id: now.id, value: r.value }], [now], source).length) continue
      flushSync(() => {
        if (applyOne(f.el, r.value)) count++
      })
    }
    setRows([])
    choose([])
    describe("")
    setFields(fieldsOf(scope))
    status("已填入 " + count + " 项，未提交。")
  }
  return (
    <div data-assist-ui="" className="my-2 basis-full min-w-0 text-sm">
      {!open ? (
        <Button
          type="button"
          size="sm"
          onClick={() => {
            setFields(fieldsOf(scope))
            show(true)
          }}
        >
          ✦ AI 辅助填写
        </Button>
      ) : (
        <Card as="section" className="space-y-2 p-3" aria-label="AI 辅助填写">
          <div className="flex items-center justify-between gap-2">
            <strong>AI 辅助填写</strong>
            <Button type="button" variant="ghost" size="sm" onClick={close}>
              关闭
            </Button>
          </div>
          {fields.length ? (
            <>
              <p>描述已知信息，不要粘贴凭据。生成后先预览，不会提交表单。</p>
              <p className="text-xs">可填：{fields.map((f) => f.label).join("、")}</p>
              <Textarea
                aria-label="填写说明"
                className="w-full"
                rows={3}
                maxLength={3000}
                value={source}
                disabled={busy}
                onChange={(e) => {
                  describe(e.target.value)
                  setRows([])
                  status("")
                }}
                placeholder={"例如：" + fields.slice(0, 2).map((f) => f.label + "：…").join("；")}
              />
              <p className="text-xs text-muted-foreground">
                AI 在浏览器本地运行，需要 WebGPU；首次生成将下载数百 MB 模型。已有「字段名：值」文本可直接解析，无需下载。
              </p>
              <div className="flex flex-wrap gap-2">
                {busy ? (
                  <Button
                    type="button"
                    size="sm"
                    onClick={() => {
                      stop()
                      status("已取消。")
                    }}
                  >
                    取消生成
                  </Button>
                ) : (
                  <>
                    <Button type="button" size="sm" disabled={!source.trim()} onClick={() => generate(true)}>
                      使用本地模型生成
                    </Button>
                    <Button type="button" size="sm" disabled={!source.trim()} onClick={() => generate(false)}>
                      解析键值文本
                    </Button>
                  </>
                )}
              </div>
            </>
          ) : (
            <p>没有可辅助填写的非敏感字段；密码和密钥请手动输入。</p>
          )}
          <p role="status">{note}</p>
          {rows.map((r) => (
            <label key={r.id} className="flex items-center gap-2">
              <Checkbox
                checked={chosen.includes(r.id)}
                onChange={(e) => choose(e.target.checked ? [...chosen, r.id] : chosen.filter((id) => id !== r.id))}
              />
              {fields.find((f) => f.id === r.id)?.label}：{r.value}
            </label>
          ))}
          {!!rows.length && (
            <Button type="button" variant="primary" size="sm" disabled={!chosen.length} onClick={apply}>
              填入所选字段（{chosen.length}）
            </Button>
          )}
        </Card>
      )}
    </div>
  )
}

export function ConsoleFormAssist() {
  const [scopes, setScopes] = useState<HTMLElement[]>([])
  const ids = useRef(new WeakMap<Element, number>())
  const serial = useRef(0)
  useEffect(() => {
    let frame = 0
    function scan() {
      frame = 0
      const next = [...document.querySelectorAll<HTMLElement>(scopeSelector)].filter(
        (s) =>
          s.getClientRects().length &&
          [...s.querySelectorAll("input,select,textarea")].some(
            (el) => !el.closest("[data-assist-ui]") && el.closest(scopeSelector) === s,
          ),
      )
      setScopes((old) => (old.length === next.length && old.every((s, i) => s === next[i]) ? old : next))
    }
    function schedule() {
      if (!frame) frame = requestAnimationFrame(scan)
    }
    scan()
    const ob = new MutationObserver(schedule)
    ob.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["hidden", "open", "aria-hidden", "class"],
    })
    window.addEventListener("resize", schedule)
    return () => {
      ob.disconnect()
      cancelAnimationFrame(frame)
      window.removeEventListener("resize", schedule)
    }
  }, [])
  return (
    <>
      {scopes.map((scope) => {
        if (!ids.current.has(scope)) ids.current.set(scope, ++serial.current)
        return createPortal(<Panel scope={scope} />, scope, String(ids.current.get(scope)))
      })}
    </>
  )
}
