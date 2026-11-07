# ZYJ test Agent 身份

本实例的完整无密钥配置是 [values.yaml](values.yaml)。SA 与集群级绑定由
`deploy/helm/doops-agent` 渲染，实例目录不再维护第二份 RBAC YAML。

| 项目 | 约定 |
|---|---|
| Gateway target | `gw-zyj-test` → `doops-zyj/zyj-test` |
| Helm release / namespace | `doops-agent-test` / `agents-test` |
| Kubernetes 身份 | `system:serviceaccount:agents-test:doops-agent-test` |
| 集群级绑定 | `agents-test-doops-agent-test-cluster-admin` → `cluster-admin` |
| 权限管理 | `rbac.create=true`，由同一 Helm release 管理 SA/CRB |
| 节点入口 | `hostAccess.enabled=true`，Gateway `exec` 审计下进入 Pod 所在节点 |

此次运维授权明确要求与 `gw-zyj` 对齐 Kubernetes 集群级权限。`agents-test` 是 Agent
运行位置，不再是它的管理范围；用户仍须具备对应 Gateway target grant。
不复制 `gw-zyj` 的 admin.conf，不改变其他 Agent 或用户的 grant，不删除旧的 namespace
RoleBinding。持有本 target grant 意味着可经 Agent 管理整个集群，包括 Secret。

安装与身份标准见 [CLUSTER_INSTALL_GUIDE.md](../../../docs/CLUSTER_INSTALL_GUIDE.md#agentgateway-target-与集群身份)；
收编与恢复见 [BUILD_DEPLOY_SOP.md](../../../docs/BUILD_DEPLOY_SOP.md#agent-rbac-纳管与回滚)。
跨越首次 RBAC 纳管点回滚必须由独立管理通道恢复；不能让 SA 自己删除自己的绑定后继续验证。

本文件的 image digest 是已验证的迁移前基线。发布时用新 CI 的固定 digest 显式覆盖，
发布结果与实际 Helm revision 记录在 [整改进度](../../../../docs/reviews/2026-11-07-agent-k8s-identity-progress.md)。
