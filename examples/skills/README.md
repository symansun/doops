# DoOps Skills 示例库

开箱即用的运维示例，全部基于 `doops` CLI（Gateway 模式）。每个示例含 `run.sh` 和 README（用法 + 测试说明）。

| 示例 | 作用 | 入口 |
| --- | --- | --- |
| nginx 巡检 | 进程状态 / `nginx -t` / 监听端口 / 错误日志尾部 | [nginx-patrol](nginx-patrol/) |
| 证书到期巡检 | `openssl s_client` 批量检查域名证书剩余天数，低于阈值非零退出 | [cert-expiry-check](cert-expiry-check/) |
| 日志收集打包 | 目标节点打包最近日志到会话工作区，`doops pull` 拉回 | [log-collect](log-collect/) |
| 发布规范 | 只发 tag、预发直接发、生产走 gateway 审批流（`--approval` 重放） | [release-flow](release-flow/) |
| 平滑发布/灰度 | 双容器 blue-green + nginx upstream 切换，零停机、可选灰度、秒级回滚 | [blue-green-release](blue-green-release/) |

通用前置条件：本机已安装 doops CLI 并 `doops login` 到目标 gateway target。新手上路先看仓库根 README 的「客户端安装与连接」。
