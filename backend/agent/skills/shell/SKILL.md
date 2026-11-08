---
name: shell
description: 进入当前 target 的宿主机，或把结构化工具未覆盖的操作交给受审计的 exec
triggers:
  keywords: [宿主机, 进入节点, host, nsenter]
phase: [analyze]
priority: 5
max_tokens: 1000
requires: []
conflicts: []
---

结构化工具没覆盖的操作，生成确定命令交给上层通过
`doops -session <s> exec --target <t>` 执行。当前 ask 会话没有 shell 工具，不绕过工具列表。

## 进入宿主机

用户在已有 target 上说“进入宿主机”，指该 Agent 所在节点。沿用当前 target/session，
先做只读身份验证；不要重新列 A/B/C 让用户选，不把它改成 GPU 检查或新 Agent 安装。
只有用户明确指向另一个物理机或 hypervisor、且映射缺失时，才询问该具体目标。

1. 用 `Read` 读取 `/app/self-docs/docs/PHYSICAL_HOST_ACCESS.md` 的“进入宿主机的固定流程”。
   采用其中的探测命令，核验 `/host`、PID 1 根目录和 mount namespace，再通过
   `nsenter -t 1 -m -u -i -n -- ...` 执行身份检查。不能只运行容器内的 `hostname`。
2. 当前 ask 没有 Bash/ExecuteCode 是既定分工，不是死路。输出一个完整的 `bash` 代码块，
   内容是待在 Agent 上执行的原始探测脚本，不包 `doops exec`，不含 `<target>` 等占位符。
   Web 的“在此执行”会预填该命令，用户提交后按当前 target/session 走 Gateway exec；
   上层有 CLI 时，将此脚本作为 `doops -session <s> exec --target <t> --cmd` 的参数，
   沿用已有授权执行。生成代码块不代表已执行，不要求用户再开 Bash 权限。
3. 执行方等待 exec 终态并检查身份输出。Web 结果显示在执行栏，不会自动回传本次 ask；
   未收到实际结果时只说明命令已准备好，不宣告成功或假装轮询。上层 CLI 按返回值验收。
   失败返回实际退出码和缺项；不要尝试只读 K8s 工具的 exec、其它 Pod、SSH 或直连端口。
   `hostAccess` 被关闭时明确报告，不能自行打开。
4. 每次后续宿主命令仍要带 `nsenter`；一次 exec 不会把后续会话永久切进宿主。
   只有本次进入和只读检查获授权，不能顺带安装、重启、改挂载或修驱动。

## 其它命令

- 系统是 Ubuntu/Debian 或 Alpine，包管理器 `apt-get` 或 `apk`。
- 长命令加 `timeout`；关键步骤失败就停（`set -eu`）。
- 临时文件放 `/tmp/doops-*`。
- 凭据从环境变量或已绑定的受管凭据文件读取，不要打印出来。
- Go 代理用 `GOPROXY=https://proxy.golang.org,direct`。
- 在 `ask --apply` 里，K8s/Helm 变更用 `mcp_doops_helm`；shell 里的 kubectl/helm 变更不算发布证据。
