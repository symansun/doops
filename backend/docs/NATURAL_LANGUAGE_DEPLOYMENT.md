# Natural-Language Deployment

DoOps v3 的发布入口是明确授权的自然语言任务，而不是环境注册表、部署模板或 workflow YAML。

正式任务必须直接包含：

- target；
- 服务范围；
- 镜像版本；
- 变更原因；
- 验收条件。

调用示例：

```bash
doops -session release-20260717 ask --target <target> --apply \
  --msg "在 <target> 发布 <服务范围>，镜像 <版本>；原因：<原因>；验收：<条件>。"
```

`--apply` 是调用端的 mutation 授权。缺少上述任何发布事实时，Agent 必须返回真实阻塞原因，不得推断 target、镜像、Helm 参数或凭据引用。运行时 Helm 资产仅从任务明确指定的 `deploy/helm/<name>/` 使用。

普通前台 `exec` 可以执行明确给出的 Helm `upgrade/install/rollback/uninstall`。
命令受 Gateway 用户 `exec` 权限、session 隔离、目标操作串行化和完整退出审计约束；
不得把 Helm 误判为危险 shell 命令。`exec --bg` 的审计只覆盖任务提交，不能承载
doops-agent Helm mutation；此类后台调用必须明确拒绝。

发布前必须比较当前 workload 与候选 Helm 资产中的 command、args、env、probes、
volumeMounts 和 volumes。只有这些运行契约完全未变的纯镜像发布，才使用结构化
`doops_k8s set-image`，并显式传入 namespace、resource、container、不可变 digest、
完整 revision 和 `confirm=true`。该操作使用 strategic merge，只修改目标容器镜像及
revision，不替换整个 containers 数组。

如果运行契约发生变化，例如删除 Agent 注册参数或环境变量，发布任务必须使用明确授权的
Helm 资产收敛完整 Deployment，再执行 rollout 和功能验收。不得用 `set-image` 跳过迁移；
shell 中的 kubectl mutation 不构成发布成功证据。
