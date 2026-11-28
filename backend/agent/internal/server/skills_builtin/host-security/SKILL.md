---
name: 主机安全与合规治理 Skill
description: 只读巡检挖矿与临时目录可疑进程、违规代理/内网穿透、SSH 加固基线与待更新安全补丁，支持灵敏度与分项开关
entrypoint: scripts/inspect.sh
exec_mode: auto
---

# 主机安全与合规治理 Skill（L2 声明式插件）

## 执行方式
- **脚本直跑（默认）**：执行 `scripts/inspect.sh`，严格只读扫描本机进程表、监听端口、`/etc/ssh/sshd_config` 与系统包管理器待更新安全补丁，百毫秒产出 Markdown + JSON 结构化安全报告（零大模型 Token）。
- **AI 安全研判**：触发「AI 安全事件研判」预设时，`do-agent` 结合本 Skill 结果核查可疑进程属主、计划任务与登录记录并形成处置清单。

## 支持的参数（`DOOPS_INPUT_*`）
- `DOOPS_INPUT_CHECK_PROXY`：是否开启违规代理/穿透进程与常见代理端口检测（默认 `true`）。
- `DOOPS_INPUT_CHECK_UPDATES`：是否只读探测系统待更新软件包数量（默认 `true`）。
- `DOOPS_INPUT_SENSITIVITY`：检测灵敏度（`standard` 或 `strict`，默认 `standard`；`strict` 模式下额外标记 `/tmp` 与 `/dev/shm` 下的可执行进程与 SSH 口令登录）。

## 约束
严格只读，不终止任何进程，不修改任何系统配置文件，绝不读取或输出任何凭据与环境变量密钥。
