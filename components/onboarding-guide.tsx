"use client"

import { useState } from "react"
import { Button, Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui"
import { ServerIcon, SparkIcon, TerminalIcon } from "./icons"

const STEPS = [
  {
    icon: ServerIcon,
    title: "从首页查看运行总览",
    body: "首页显示连接、会话、集群与近期活动。在「监控与态势」查看态势感知、资源监控与 Agent 运行监控，在「自动化」统一管理定时任务、智能巡检与执行记录，或在「资产台账」维护机器、集群、业务系统与各类资源。",
  },
  {
    icon: TerminalIcon,
    title: "从右上角选择目标 Agent",
    body: "顶栏右上角显示当前目标，随时可以切换或返回全部 Agent。选择后，「Agent 概览」集中呈现节点状态、资源对账清单与台账分配范围。新 Agent 的安装步骤在「发布部署 → 首次接入」。",
  },
  {
    icon: SparkIcon,
    title: "查看资源或执行操作",
    body: "左侧菜单按用途列出功能：数据库、网站、Kubernetes、镜像和文件直接打开。「AI 助手」用自然语言描述需求，「终端」直接执行 shell 命令。",
  },
]

export function OnboardingGuide({ onClose }: { onClose: () => void }) {
  const [step, setStep] = useState(0)
  const cur = STEPS[step]
  const Icon = cur.icon
  const last = step === STEPS.length - 1

  return (
    <Dialog open onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent size="md" className="p-6">
        <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15 text-primary">
          <Icon width={24} height={24} />
        </div>
        <DialogTitle className="text-xl font-semibold tracking-tight text-card-foreground">
          {cur.title}
        </DialogTitle>
        <DialogDescription className="mt-2 text-sm leading-relaxed text-muted-foreground">
          {cur.body}
        </DialogDescription>

        <div className="mt-6 flex items-center justify-between">
          <div className="flex items-center gap-1.5" aria-hidden="true">
            {STEPS.map((_, i) => (
              <span
                key={i}
                className={`h-1.5 rounded-full transition-all ${
                  i === step ? "brand-gradient w-5" : "w-1.5 bg-muted"
                }`}
              />
            ))}
          </div>
          <div className="flex items-center gap-2">
            <Button onClick={onClose} variant="ghost">
              跳过
            </Button>
            {step > 0 && (
              <Button onClick={() => setStep((s) => s - 1)}>
                上一步
              </Button>
            )}
            <Button
              onClick={() => (last ? onClose() : setStep((s) => s + 1))}
              variant="primary"
            >
              {last ? "开始使用" : "下一步"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
