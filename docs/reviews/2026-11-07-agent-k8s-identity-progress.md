---
title: Agent 与 Kubernetes 身份整改进度
date: 2026-11-07
owner: doops-maintainers
status: in-progress
---

# 目标与边界

延续已授权的 `gw-zyj-test` 集群权限整改，把身份与 RBAC 纳入可回读、可回滚的 Helm 配置。
工作树为 `claude/agent-k8s-identity`；主工作区中的其他改动不属于本任务。
不变更 Gateway 用户、token、grant，不变更 `gw-zyj`、业务工作负载或其他集群的权限。
凭据保留在用户私有配置目录，证据和 Git 不含凭据 payload。

# 现场基线

- CLI 与 test Agent 均为 `f5cebcdabb37ca6459271cc8487c6c00ac7dcbbe`。
- target 为 `doops-zyj/zyj-test`；release 为 `agents-test/doops-agent-test` revision 8。
- Pod 为 `doops-agent-test-7cb959dcd-kmf7v`，Ready，重启次数 0。
- 身份为 `system:serviceaccount:agents-test:doops-agent-test`，`kubeconfig.source=serviceAccount`。
- 原先失败的四项 `auth can-i` 及 `* * --all-namespaces` 现均为 yes。
- `agents-test-doops-agent-test-cluster-admin` 已存在并指向该 SA，但仅贴了 Helm ownership
  和 `keep` 注解。revision 8 manifest 只有 Deployment；SA 也没有 Helm ownership。
  因此当前权限已生效，但尚未真正成为 release 管理的资源，升级/卸载无法完整管理其生命周期。

# 实施与验收计划

1. 保存当前 Helm revision、values、manifest、SA、绑定和运行状态；检查备份权限及摘要。
2. Chart 增加显式 `rbac.create`，默认关闭；启用时只允许 dedicated ServiceAccount 模式，
   创建 SA 与 cluster-admin 绑定。不用 `keep` 保留孤立权限。文档说明 Gateway target
   与 Kubernetes 身份、宿主机访问的不同职责；为 test 保留无密钥部署 values。
   检查：Helm 实际渲染、非法组合拒绝、现有 mounted/外管 SA 模式没有隐式提权。
3. 通过独立于 test Agent 身份的审计控制通道收编资源并进行 Helm 收敛，先 server dry-run，
   后 mutation；失败恢复 revision 和收编前资源，不能靠 test 自己已被撤销的权限恢复。
   检查：release manifest 真正包含三类资源、身份不变、四项权限和真实跨 namespace 读取、
   API server dry-run 创建、Pod Ready、Gateway 回连、镜像 digest 与 commit。
4. 自查与独立审查、相关本地检查、CNB CI、制品及远端提交可见性；记录证据与回滚入口。

# 推进约束

最多 12 次实现/验证迭代，90 分钟；连续 3 次同一阻塞且无新证据则停止 mutation 并交接。
任一权限对象指向意外主体、release owner 冲突、revision 漂移或 RPC 终态不明时停止，先回读。
禁止削弱测试、重放终态不明的 mutation、删除其他 agent 的权限或输出凭据。

# 结果

进行中。线上权限现状已确认；尚未执行本轮 release mutation。

- 当前备份：用户私有 `~/.agent/doops-lwy/backups/zyj-test-rbac-20261107/`，目录 0700、文件 0600。
  `before.json` SHA256 为 `c815c9aae313d0570af1c18afb16a7441ac783741e53cd927040222563abe311`。
  原有 2026-11-06 两份备份保留。
- Chart 0.3.0 显式管理 SA/CRB，默认无提权，非法 source/SA/布尔参数在渲染时失败。
- 发现并修复跨纳管点自回滚会删除控制器身份的缺口；共享 guard 覆盖预检、apply、rollback。
- 本地初轮契约 328 项通过，CLI package tests 通过；新增 guard 与完整 Agent 验证待完成。
- 线上合成 opaqueSecret 已验证 create/put(stdin)/grant、未验证 promote 拒绝、verify/promote、
  target list 与未绑定 target 隔离、payload 不回显。发布后检查持久性并 revoke，临时资源未交付。
