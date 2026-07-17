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
