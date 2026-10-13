# HDU 升级入口收敛记录

## 目标与验证

范围：对照用户提供的 HDU 升级记录，修复客户端半安装问题，纠正部署入口误导。
在 main 工作目录修改；不创建 worktree，不修改线上资源或权限。

1. 完成：读取安装器、外部发布脚本、CLI 和权威文档，对照附件逐项核查。
2. 完成：仅有旧配置时，候选 CLI 在替换文件前拒绝安装；隔离配置不能绕过此检查。
   验证：安装器/发布契约 25 项测试通过，CLI 旧配置测试通过。
3. 完成：入口集中到 `CLUSTER_INSTALL_GUIDE.md`，README、Skill、Usage 和 SOP 同步纠错。
   验证：独立只读审查、脚本语法和 diff 检查。
4. 未完成：HDU 线上恢复与候选镜像发布。本轮没有执行 mutation。

## 线上证据

通过已有管理机登录执行：

```bash
doops -session hdu-upgrade-review-20261013 targets --target gw-hdu-235
doops -session hdu-upgrade-review-20261013 exec --target gw-hdu-235 --cmd 'hostname; /app/doops-agent -h 2>&1 | head -n 12; command -v helm; command -v kubectl; test -s /root/.kube/config && printf "shared_kubeconfig=present\n"'
```

目标 `doops-235/hdu-235` 可见、idle，`RELEASE_COMMIT=<unknown>`；
exec 返回 `execution_outcome_unknown`。没有远端命令成功证据，
不能据此断言 kubeconfig、Agent 版本或升级控制器可用。
附件中缺登录态的问题只适用于当时新建的隔离配置，不适用于当前管理机。

## 尚未解决的发布实现限制

- 已有 release 的外部脚本强制 Gateway 登录，不支持 kubeconfig-only 恢复。
- 外部脚本 `--version` 依赖 Docker 拉镜像；K3s/containerd 不提供 Docker。
- 外部脚本仍用请求 OCI digest 直接比较 CRI imageID，多架构镜像可能错误拒绝。
- CLI 和外部脚本都以本机 CLI commit 验收候选 Agent，不能任意组合版本。

这些限制已明确写入入口文档，没有通过跳过鉴权或验收来掩盖。下一步恢复必须解决
实际镜像身份校验并从外部 Kubernetes 管理上下文验证，不能重复在坏命令通道上发升级。
