---
name: GPU 算力治理 Skill
description: 只读巡检 NVIDIA GPU 卡数、利用率、显存占比、温度、功耗、ECC 错误与占卡进程，支持自定义温度与显存阈值
entrypoint: scripts/inspect.sh
exec_mode: auto
---

# GPU 算力治理 Skill（L2 声明式插件）

## 执行方式
- **脚本直跑（默认）**：执行 `scripts/inspect.sh`，调用只读 `nvidia-smi`（在 `hostPID=true` 容器内自动以只读 `nsenter -t 1 -m -- nvidia-smi` 探测宿主机 GPU），百毫秒返回标准 Markdown + JSON 产物（含 `metrics[]`、`checks[]`、`charts[]`、`gpus[]`、`gpu_processes[]`），零大模型 Token 消耗。
- **AI 深度排查**：点击「排查僵尸占卡」等 AI 预设或附加自然语言提示词时，`do-agent` 结合本 Skill 实时定位异常占卡任务并给出治理建议。

## 支持的参数（`DOOPS_INPUT_*`）
- `DOOPS_INPUT_WARN_TEMP_C`：GPU 温度告警阈值（默认 `85` °C，严重阈值为 `+7` °C）。
- `DOOPS_INPUT_WARN_MEM_PCT`：显存占用告警阈值（默认 `90` %，严重阈值为 `98` %）。
- `DOOPS_INPUT_INCLUDE_PROCESSES`：是否采集占卡计算进程短名与显存占用（默认 `true`，仅输出 `comm` 短名，不采集敏感命令行参数）。

## 约束
严格只读；无 GPU 宿主机返回 `pass` 与 `gpu_count=0`，不伪造利用率或温度指标。
