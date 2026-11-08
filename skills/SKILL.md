---
name: doops-server
description: |
  用 doops 连接和操作 doops-agent 节点。doops 是 SSH 的替代入口，默认通过
  doops-gateway 进入内网 agent，执行已明确范围的检查、文件同步与运维任务。
---

## Skill 定位

本 skill 是 doops 的入口导航和操作纪律，不承载所有功能细节。

- 安装与接入：先看本文，再按 `docs/CLUSTER_INSTALL_GUIDE.md` 执行。
- 日常使用：先看本文命令选择，再按 `docs/DOOPS_USAGE_GUIDE.md` 执行。
- Gateway/API 细节：看 `docs/DOOPS_GATEWAY_TUNNEL.md` 和 `docs/DOOPS_API_GUIDE.md`。
- 构建、发布、升级：看 `docs/BUILD_DEPLOY_SOP.md` 和 `docs/DOOPS_MAINTENANCE_LOG.md`。
- 模型与 ask 配置：看 `docs/MODEL_STANDARDS.md`。

权威源码在 `symansun/doops` 仓库 `skills/SKILL.md`。安装副本中的 `docs/` 相对 Skill
目录读取，源码文档位于仓库根 `docs/`。维护时先核验源码 checkout 的远端和版本，再从其根目录
运行 `scripts/`；安装副本不包含脚本，不要误用业务项目的同名脚本或补写替代脚本。
日常 CLI 无需源码 checkout；源码缺失只阻塞需要它的维护动作。

## 核心纪律

- 标准安装路径是 `~/.local/bin/doops`；先核对实际入口和帮助，不执行仓库内未知版本的二进制。
- 所有远端调用显式带 `-session <name>`；工作区操作靠它隔离，同一任务复用，不跨任务共用。
  `targets`、管理员 HTTP 命令虽可带该全局参数，却不使用 session 隔离；`list` 只读本地配置。
- 默认使用 Gateway 模式；日常 target 必须是 gateway target。
- SSH 只用于首次安装、自恢复 agent，以及发布 `doops-gateway` 控制面；不是日常 target 操作入口。
- `doops-gateway` 是独立发布包，必须用 `scripts/deploy-gateway.sh --host <gateway-host>` 从 SSH 宿主机上下文发布。
- gateway 控制面不登记为 doops target，不通过任何 doops target 发布 gateway 本体。
- 不要用 base64、tar 或手写分片传输绕过 `push`。`doops_workspace_*` 是低层兼容 JSON-RPC 工具，不是 CLI、发布脚本和 skill 的标准路径；如果看到 512KB 分块日志，说明调用方绕过了当前 `doops push/pull`。应修复 Git HTTP 反向隧道、目标 agent 版本或发布脚本。
- 不要混用 token：
  - gateway CLI 操作用 gateway user token。
  - agent 注册 gateway 不需要 agent token；只需要 gateway URL、cluster 和 instance。
  - 直连 agent token 只属于遗留直连路径，不写入标准 target 配置。
- 不要在参数、prompt、日志、终端、文档或提交里暴露凭据。token 签发会输出秘密，
  必须按使用指南直接捕获到授权的私密文件；`--save-as` 仍输出 token，并覆盖指定缓存。
- `exec/ask` 成功后可能自动把远端 `deploy.sh` 覆盖到本地当前目录，即使任务只是只读。
  调用前切到本任务专用目录，不能有需保留的同名文件；不要删除已有文件规避检查。
- `pull` 会提交远端工作区快照；本地目的地已有 `.git` 时会 `reset --hard`。
  默认使用全新下载目录，不指向业务仓库；复用前核对所有权与未提交改动。
- `ask --msg` 必须写入 target、对象、已有授权、限制和验收。只读诊断明确禁止修改、
  重启、部署和清理；范围内实施无需重复确认，新增影响交回上层。下游会自动批准工具，
  不能假设它替你再次询问；精确已知检查优先 `exec`。
- 命令提交、模型摘要和 `Operation complete.` 不证明业务成功。核对终态、退出码及实际资源；
  超时、取消或断连后状态不明时报告未知，不自动重放变更。
- 如果用户是在发账号、发 token、授权、查审计、清理审计、查看/取消 gateway active operations，要按 gateway 管理动作处理，不要混成普通 target 运维。
- 遇到 `user operation limit exceeded` 时，先用 `doops -session <s> admin operations list --target <gateway-target>` 查占槽操作；确认 stale 且取消在授权范围内后，用 `doops -session <s> admin operations cancel --target <gateway-target> --id <operation-id>` 取消。升级后的 agent 会按进程组终止 shell 类命令；`ask` 需要额外核查 doagent session 是否仍在工作。
- 给用户签发 gateway user token 时，优先使用 `doops -session <s> admin token create`；不要 SSH 到 gateway 主机手工运行内部 DB 命令。

## 配置与凭据

DoOps CLI 默认只有一个配置源：

```text
~/.agent/skills/doops/config.json
```

同目录运行态文件：

- `auth.json`：`doops login/logout` 管理的 token 缓存，结构必须是 `{"tokens":{"target-name":"..."}}`。
- `history.jsonl`：本机命令历史。
- `sessions.json`：本机会话缓存。

旧路径和运行痕迹不能作为当前配置依据：

- `~/.config/doops/config.json`
- `~/.doops/auth.json`
- 项目 `.doops/`

`DOOPS_CONFIG=/path/to/config.json` 只用于测试和应急显式覆盖。

标准配置文件只放 gateway target：

```json
{
  "servers": [
    {
      "name": "jm",
      "aliases": ["jy"],
      "gateway": "https://gateway.example.com",
      "cluster": "cluster-c",
      "instance": "node-3",
      "use": "JM via gateway",
      "token": "<GATEWAY_USER_TOKEN>"
    }
  ]
}
```

## 安装引导

安装 CLI 和 skill：

```bash
git clone https://github.com/symansun/doops.git
cd doops
bash scripts/install.sh --project /absolute/path/to/project
```

安装结果：

- CLI：`~/.local/bin/doops`
- Skill 副本：`<project>/.agent/skills/doops/SKILL.md`
- Canonical config：`~/.agent/skills/doops/config.json`

接入 gateway target 前核对 Gateway URL、cluster、instance 和凭据交付方式。
当前 `add` 要求 `--token`，尚无 stdin 输入参数；不要让 Agent 将真实 token 展开到命令行。
已有 target 可由用户在私密交互终端运行 `doops -session <s> login --target <t> --username <u>` 输入密码。
无安全输入通道时报告该具体限制，不编造 `--token-stdin`，不临时切成直连 target。

首次安装 agent 到新机器、K8s/DaemonSet 接入、裸二进制迁移等细节见 `docs/CLUSTER_INSTALL_GUIDE.md`。如果必须临时排查遗留直连节点，要在问题结论里明确标注它不是标准 gateway target。

## 使用引导

先确认本地配置和 gateway 在线状态：

```bash
doops list
doops -session inspect targets --target <gateway-target>
```

常用命令：

| 需求 | 命令 |
| :--- | :--- |
| 执行确定性命令 | `doops -session <s> exec --target <t> --cmd '<cmd>'` |
| 只读诊断 | `doops -session <s> ask --target <t> --msg '只读检查 <对象>；禁止修改、重启、部署和清理，返回证据'` |
| 推送本地目录 | `doops -session <s> push --target <t> --src <dir>` |
| 拉取远端工作区 | `doops -session <s> pull --target <t> --dest <new-download-dir>` |
| 小文本读写 | `doops -session <s> read --target <t> --path <file>`；`doops -session <s> write --target <t> --path <file> --file <local>` |
| 工作区清理 | `doops -session <s> clean --target <t>` |
| 升级在线 agent | `doops -session <s> upgrade ... --dry-run` |
| 管理员签发 token | `doops -session <s> admin token create ...`，先按使用指南准备私密输出 |

大文件、压缩包、数据库导出、图片、视频、课程资源包等资产，用 `pull` 拉取整个 session 工作区。不要用 `read`，也不要让 `exec` 输出 base64。

Gateway 管理员签发 token 前确认用户、有效期及交付位置；不要直接执行会把秘密返回工具的裸命令。
`--target` 选择 Gateway 和管理员身份；`--save-as` 只管理缓存，不能隐藏输出。

完整命令说明和示例见 `docs/DOOPS_USAGE_GUIDE.md`。

## 升级引导

升级 doops CLI、gateway、agent 或 skill 前，先定位其源码版本、目标与验收条件。
安装器会替换 CLI 和 Skill/docs；应先保留需要的旧版本及本地改动，安装失败不继续部署。
源码维护记录位置为：

```text
docs/DOOPS_MAINTENANCE_LOG.md
```

升级前检查：

- 唯一配置源仍是 `~/.agent/skills/doops/config.json`。
- `auth.json` 使用 `tokens` 字段，没有写到旧的 `~/.doops/auth.json` 或 `passwords` 字段。
- `doops -session <s> targets --target <gateway-mode-target>` 只展示当前有效 gateway targets。
- 旧 target、旧 alias、旧直连节点、短 token 测试配置已经淘汰。
- `.agents/`、`.doops/`、`auth.json`、`config.json`、`history.jsonl`、`sessions.json` 没有混进仓库。
- 文档里的镜像 tag 和发布策略一致；日常只更新轻镜像，不应反复拉大 base。
- gateway 控制面发布走 `scripts/deploy-gateway.sh`；不要走 `doops exec/push/write`。
- 按任务环境回归受影响路径，记录实际版本、退出状态和验收证据；dry-run 或上传成功不等于升级完成。

发布和构建细节见 `docs/BUILD_DEPLOY_SOP.md`。

## 故障入口

- `401 Unauthorized`：认证失败。先确认 CLI 读的是 `~/.agent/skills/doops/config.json`，标准 target 的 token 必须是 gateway user token。
- `403 Forbidden`：认证通过但权限不足，或用户被显式收窄。
- target 不可见：先跑 `doops -session <s> targets --target <gateway-target>`，区分离线与权限过滤。
- `busy=true`：表示 target-wide 阻塞；`status=active` 且 `busy=false` 表示有 session 在跑，但其它 session 仍可使用。
- gateway `push/pull` 失败：确认 `/v1/git/<cluster>/<instance>/<session>.git` 能路由到在线 agent，并检查 agent 本地 `/git/` handler/token。

更完整的 API、隧道和故障细节见 `docs/DOOPS_GATEWAY_TUNNEL.md`、`docs/DOOPS_API_GUIDE.md`。
