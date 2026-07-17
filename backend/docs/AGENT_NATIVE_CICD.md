# Agent-Native CI/CD

DoOps v3 的 CI/CD 输入是自然语言任务，不使用 Environment、DeploymentTemplate、
ServiceRelease 或 YAML workflow。

唯一入口是普通 Ask：

```text
doops -session <session> ask --target <configured-target> --msg '<发布任务>'
```

发布任务必须明确：

- target；
- 服务范围；
- 镜像版本或不可变 digest；
- 变更原因；
- 验收条件；
- 是否已经授权执行 mutation。

CLI 只负责会话和 target 路由。Gateway 负责认证、授权、资源锁和工具审计。
doagent 选择 `$doops-cicd`，根据任务和运行时实际暴露的工具执行诊断或发布。

未授权 mutation 时只能观察。缺少目标、镜像、服务范围或验收条件时必须报告阻塞，
不得从目录名、域名、历史版本或遗留 YAML 推断参数。成功结论必须来自本轮真实工具
输出，不能使用本地 workspace 快照、workspace commit、远端 source revision 校验或
Agent 自报的合成证据。

## Code Map

| Responsibility | Code |
| :--- | :--- |
| Ask CLI | `backend/skills/doops-cli/cli/doops/main.go` |
| Ask routing and ACP event forwarding | `backend/agent/internal/server/handler_ws.go` |
| Gateway authorization and resource locking | `backend/agent/internal/server/tunnel_hub.go` |
| CI/CD Skill | `backend/agent/skills/doops-cicd/SKILL.md` |
