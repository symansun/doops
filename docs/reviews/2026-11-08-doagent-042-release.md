# DoAgent 0.2.42 test 发布验收

本次发布 DoAgent 的 agent_loop 模块拆分及权限 hook 修复。DoOps 运行时锁定新
DoAgent 制品，通过既有 Agent 自升级控制器发布到 `gw-test`，即
`doops-zyj/zyj-test`。未升级其他 target。

## 源码与 CI 制品

| 项目 | 证据 |
|---|---|
| DoAgent 仓库 / tag | `l8ai/ai/doagent` / `v0.2.42` |
| DoAgent main commit | `08ecb0193767f559844d985cc7365484c61bf9c5`（PR #46） |
| DoAgent main CI | `cnb-hvh-1k6sa86h3`，success |
| DoAgent tag CI | `cnb-qb9-1k6sa8oqh`，success |
| DoOps 仓库 / tag | `l8ai/ai/doops.sh` / `v1.3.123` |
| DoOps 制品 commit | `8683d67174e19a1750e9b19de99a0816cdb8ea79` |
| DoOps main CI | `cnb-tci-1k6sdvv6i`，success |
| DoOps release CI | `cnb-rl3-1k6sg8rga`，success |
| 实际运行镜像 | `docker.cnb.cool/l8ai/ai/doops.sh@sha256:fd20c510411d9dde8e2061ed390e3b91877781ade2d4400a39b715280524b02a` |

本地生产特性 release gate：1,603 个唯一 lib 测试通过，5 个既有 ignored
未执行；agent_loop 142 项。CLI 版本契约、MiniMax CLI/ACP 真实模型调用、
真实 Bash marker 验证均通过。默认模型缺失本地密钥文件和 OpenAI 403 属于
已记录的配置失败，不能计为通过；实际使用 MiniMax 完成验证后恢复了临时配置。

## 部署回读

2026-11-08 经 `doops -session doagent-release-20261108` 回读：

- CLI 与在线 Agent 都是 `v1.3.123`，完整 commit 与上表一致。
- Helm release `doops-agent-test`，namespace `agents-test`，revision **14**，
  状态 `deployed`；Deployment rollout 成功，Ready **1/1**。
- Gateway 实际服务 Pod `doops-agent-test-5b9465d594-nggqx`，
  UID `5ca1b839-9258-47dc-9b34-ec5503fedc2d`，spec image 与 imageID 均匹配上表 digest。
- `/usr/local/bin/do-agent --version` 返回 `do-agent 0.2.42`；
  ACP `http://127.0.0.1:9000/health` 返回
  `{"protocol":"acp","status":"ok","version":"0.2.42"}`。
- 经同一 Gateway 的 `exec` 执行
  `nsenter -t 1 -m -u -i -n -- hostname` 成功，返回 `VM-0-11-ubuntu`。
- `doops-upgrade-verified-doops-agent-test` ConfigMap 的 revision 14
  evidence 同时记录上述 Pod UID、节点 `vm-0-11` 和 digest。
- 浏览器在 `zyj-test` 的 AI 助手实际提交只读验收请求，执行轨迹显示
  `mcp_doops_k8s_read` 完成并返回 Deployment Ready 1/1。

## 回滚与验收边界

本次成功升级前的基线是 revision **13**（曾回滚到 revision 11），镜像为
`docker.cnb.cool/l8ai/ai/doops.sh@sha256:e926271555dfad6eacc454ae31fab6c1bb4e9de52173bd68ceadc364480919dd`，
对应 `v1.3.121`。该 revision 的验证记录保存在同一 ConfigMap。
若需要回滚，使用审计入口
`doops -session doagent-042-rollback upgrade --target gw-test --rollback`，
完成后重新核对 Gateway、实际 Pod imageID、版本与功能；不在 Agent 进程中替换自身。

本次通过不代表原始 DoOps 固定工具白名单问题已解决。承载版本仍限定 AI
可用工具，`exec/nsenter` 的成功只证明宿主机执行通路可用，不证明 AI 能自行选用。
浏览器模型名称显示 `minimax/unconfigured`，但实际模型请求及工具调用完成；
该显示问题也不在本次 DoAgent runtime 发布修复范围内。
