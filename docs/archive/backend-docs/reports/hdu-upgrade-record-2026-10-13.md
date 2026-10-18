# HDU 升级记录（2026-10-13）

归档自 `backend/docs/CLUSTER_INSTALL_GUIDE.md`。

本次目标是 `gw-hdu-235`，身份 `doops-235/hdu-235`，已有 Helm release
`ai/doops-agent`；操作机 K3s kubeconfig 是 `/etc/rancher/k3s/k3s.yaml`。
附件中的 `v1.3.48` 是该次选定版本，不是本文自动追踪的“最新版”。

- `install.sh` 只更新 CLI/Skill，成功也不代表 Agent 已升级。安装器必须在替换前
  用候选 CLI 检查 canonical 配置和遗留配置；不能装完后才发现配置不可用。
- 当前管理机的 Gateway 登录可查询 HDU，目标显示 idle，但 `RELEASE_COMMIT`
  为 `<unknown>`，只读命令返回 `execution_outcome_unknown`。这不是完整健康状态，
  不能据此声称 `doops upgrade` 已具备执行条件。
- 外部脚本的已有 release 模式确实依赖 Gateway 登录，不能宣传为免登录一键部署；
  本机 kubeconfig 授权与 Gateway 操作员授权是两个独立边界。
- 外部脚本 `--version` 当前调用 Docker 拉取镜像解析 digest。K3s/containerd
  不等于安装了 Docker；此类操作机使用已核验的 `--image`，不要仅为解析版本安装 Docker。
- 外部脚本目前直接比较请求 digest 与 CRI `imageID`，且用本机 CLI commit
  校验新 Agent commit。OCI index、平台 manifest 和 CRI imageID 可能不同；
  这两项限制修复前，不能将附件中的 index digest 命令标为已验证的一键恢复命令。

外部恢复的参数形式如下，**这是入口说明，不是 HDU 已通过验收的命令**：

```bash
bash scripts/release-managed-agent.sh \
  --target gw-hdu-235 --session hdu-agent-recovery \
  --cluster doops-235 --instance hdu-235 \
  --release doops-agent --namespace ai \
  --kubeconfig /etc/rancher/k3s/k3s.yaml \
  --image <repository>@sha256:<verified-ci-digest> \
  --dry-run
```

dry-run 必须通过，且镜像身份验收限制必须解决后再执行正式恢复。不能用临时 target、
复制 token、跳过验收或把失败标为成功来完成升级。
