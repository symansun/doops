"use client"
import { useState } from "react"
import { assetRequest } from "@/lib/assets"
import type { CatalogKind } from "@/lib/console-views"
export function LegacyAssetNotes({kind}:{kind:Exclude<CatalogKind,"credentials"> | "vm"}) {
 const [notes,setNotes]=useState<{id:string;name:string;environment:string}[]|null>(null),[error,setError]=useState("")
 return <details className="shrink-0 border-t px-4 py-2 text-sm" onToggle={e=>{if(e.currentTarget.open){setError("");assetRequest<{records:NonNullable<typeof notes>}>("records",undefined,new URLSearchParams({kind})).then(r=>setNotes(r.records)).catch(e=>setError(e.message))}}}><summary className="cursor-pointer">旧版个人登记（仅自己可见）</summary><p>旧登记缺少资源标识，不会自动写入共享台账。</p>{error?<p role="alert">{error}</p>:notes?<ul className="max-h-32 overflow-auto">{notes.length?notes.map(n=><li key={n.id}>{n.name} · {n.id} · {n.environment}</li>):<li>没有旧登记。</li>}</ul>:<p>正在读取…</p>}</details>
}
