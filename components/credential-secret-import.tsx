"use client"

import { Button, Input, Modal } from "./ui"
import { useState } from "react"
import type { Session, Target } from "@/lib/client"
import { randomSession } from "@/lib/gateway"
import {
  buildSecretImportRequest,
  describeSecretImportError,
  importCredentialFromSecret,
  type CredentialResource,
  type CredentialVersion,
} from "@/lib/credentials"
import { SaveIcon } from "./icons"

// Mirrors `doops credential configure --from-secret <name> --keys <k,...>
// [--key-map src=dst]`. Only the Secret name, key names and key mapping leave
// the browser; values move Agent -> Gateway on the private control channel and
// the response carries version metadata only.
export function SecretImportDialog({
  session,
  target,
  credential,
  onClose,
  onDone,
  onError,
}: {
  session: Session
  target: Target
  credential: CredentialResource
  onClose: () => void
  onDone: (version: CredentialVersion) => void
  onError: (message: string) => void
}) {
  const modelProvider = credential.type === "modelProvider"
  const [namespace, setNamespace] = useState("default")
  const [secretName, setSecretName] = useState("")
  const [keys, setKeys] = useState("")
  const [keyMap, setKeyMap] = useState("")
  const [busy, setBusy] = useState(false)
  const [sessionID] = useState(() => randomSession("credential-import"))

  async function submit() {
    onError("")
    let body
    try {
      body = buildSecretImportRequest(target, sessionID, credential.type, { namespace, secretName, keys, keyMap })
    } catch (err) {
      onError(describeSecretImportError(err))
      return
    }
    setBusy(true)
    try {
      onDone(await importCredentialFromSecret(session, credential.id, body))
    } catch (err) {
      onError(describeSecretImportError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={`从目标 Secret 导入 · ${credential.name}`}
      size="lg"
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose} size="sm">
            取消
          </Button>
          <Button
            onClick={submit}
            disabled={busy || !secretName.trim() || !keys.trim()}
            variant="primary"
            size="sm"
          >
            <SaveIcon width={14} height={14} /> {busy ? "导入中" : "导入为暂存版本"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <p className="text-xs text-muted-foreground">
          只提交 Secret 名、键名和键映射。Agent 在目标上读取所选键，经私有控制通道交给 Gateway
          加密保存为暂存版本；值不会经过浏览器，也不会在页面或 API 中回显。源 Secret 不会被修改。
        </p>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="flex flex-col gap-1 text-xs text-muted-foreground">
            目标
            <div className="min-h-9 rounded-md border border-input bg-muted/40 px-3 py-2 font-mono text-xs text-foreground">
              {target.cluster}/{target.instance}
            </div>
          </div>
          <Field label="命名空间" value={namespace} onChange={setNamespace} placeholder="default" />
          <Field label="Secret 名" value={secretName} onChange={setSecretName} placeholder="app-database" />
          <Field
            label={modelProvider ? "settings.json 所在键" : "键名（逗号分隔）"}
            value={keys}
            onChange={setKeys}
            placeholder={modelProvider ? "settings.json" : "DB_URL,DB_USER,DB_PASSWORD"}
          />
        </div>
        {!modelProvider && (
          <Field
            label="键映射（可选，源键=运行时键，逗号分隔）"
            value={keyMap}
            onChange={setKeyMap}
            placeholder="DB_URL=SPRING_DATASOURCE_URL"
          />
        )}
        <p className="text-xs text-muted-foreground">
          导入只生成暂存版本。绑定当前目标并验证通过后，再激活该版本。
        </p>
      </div>
    </Modal>
  )
}

function Field({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
}) {
  return (
    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
      {label}
      <Input
        className="font-mono text-xs"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        spellCheck={false}
        autoComplete="off"
      />
    </label>
  )
}
