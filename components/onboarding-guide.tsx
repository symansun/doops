"use client"

import { Button } from "./ui"
import { useState } from "react"
import { ServerIcon, SparkIcon, TerminalIcon } from "./icons"

const STEPS = [
  {
    icon: ServerIcon,
    title: "欢迎使用 Doops 控制台",
    body: "这是一个让运维像聊天一样简单的平台。下面用三步带你快速上手，全程不到一分钟。",
    tip: "如果你只是想先看看效果，可以直接关闭本引导，使用演示数据探索。",
  },
  {
    icon: TerminalIcon,
    title: "第一步：选择一台已接入的机器",
    body: "在左侧选择 target 后再开始运维。列表为空时，说明这个账号还没有可见的 target 授权，或对应 Agent 不在线；Agent 接入与授权由管理员在 Gateway 侧完成。",
  },
  {
    icon: SparkIcon,
    title: "第二步：开始运维",
    body: "在左侧选中机器后，打开「AI 对话」用中文描述需求，或在「终端」直接执行命令。「概览」页随时查看机器健康状态。",
    tip: "试试在 AI 对话里输入：帮我看看磁盘空间还剩多少。",
  },
]

export function OnboardingGuide({ onClose }: { onClose: () => void }) {
  const [step, setStep] = useState(0)
  const cur = STEPS[step]
  const Icon = cur.icon
  const last = step === STEPS.length - 1

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/70 p-4 backdrop-blur-sm">
      <div className="w-full max-w-lg rounded-xl border bg-card p-6 shadow-xl">
        <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15 text-primary">
          <Icon width={24} height={24} />
        </div>
        <h2 className="text-xl font-semibold tracking-tight text-card-foreground">{cur.title}</h2>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{cur.body}</p>

        {cur.tip && (
          <p className="mt-4 rounded-lg bg-primary/10 px-3 py-2 text-xs text-primary">{cur.tip}</p>
        )}

        <div className="mt-6 flex items-center justify-between">
          <div className="flex items-center gap-1.5">
            {STEPS.map((_, i) => (
              <span
                key={i}
                className={`h-1.5 rounded-full transition-all ${
                  i === step ? "w-5 bg-primary" : "w-1.5 bg-muted"
                }`}
              />
            ))}
          </div>
          <div className="flex items-center gap-2">
            <Button
              onClick={onClose}
              variant="ghost"
            >
              跳过
            </Button>
            {step > 0 && (
              <Button
                onClick={() => setStep((s) => s - 1)}

              >
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
      </div>
    </div>
  )
}
