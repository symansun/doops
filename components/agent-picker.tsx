"use client"

import { Fragment, useState, type ReactNode } from "react"
import { targetIdentity, type TargetRef } from "@/lib/console-refresh"
import { Select } from "./ui"

export function AgentPicker<T extends TargetRef>({ targets, label, children }: {
  targets: readonly T[]
  label: string
  children: (target: T) => ReactNode
}) {
  const [choice, setChoice] = useState("")
  const selected = targets.find(target => targetIdentity(target) === choice)
  return <>
    <label className="block max-w-sm text-sm">{label}
      <Select value={selected ? choice : ""} onChange={event => setChoice(event.target.value)}>
        <option value="">请选择 Agent</option>
        {targets.map(target => {
          const id = targetIdentity(target)
          return <option key={id} value={id}>{id}</option>
        })}
      </Select>
    </label>
    {selected && <Fragment key={choice}>{children(selected)}</Fragment>}
  </>
}
