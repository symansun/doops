# Agent-Native CI/CD

DoOps CI/CD 只有一条运行路径：

```text
explicit declaration
  -> resolve declared repository/branch/immutable commit
  -> Gateway credential prepare
  -> metadata-only CredentialRun
  -> Gateway-bound Ask
  -> doagent $doops-cicd
```

不得改走直连 Agent、SSH、手工 Helm、临时脚本或静默兼容分支。

## CLI Contract

```text
doops -session <session> cicd run -f <manifest.yaml> \
  -target <gateway-target> [--dry-run] [--set key=value ...]
```

`target` 和 `session` 必须显式提供。CLI 从声明解析 HTTPS 源仓库、branch 和不可变
commit；没有固定 revision 时只允许通过 `git ls-remote` 将 branch 解析为一个
commit。CLI 不把本地 Secret、token 或任意工作区快照作为发布源。

在 Ask 前，CLI 调用 `POST /v1/credentials/prepare`。Gateway 使用目标 Agent 的
`doops.sh/credential-plan/v2` 编译声明、校验授权并返回不含 Secret 内容的
`CredentialRun`。dry-run 的 mutation count 必须为 0；apply 的凭据物化发生在
Ask 前。

## Prompt Binding

CI/CD instruction 的固定形状如下：

```json
{
  "task": "execute-doops-cicd",
  "skill": "$doops-cicd",
  "executionMode": "dry-run",
  "manifestPath": "deploy/release.yaml",
  "workspaceCommit": "<immutable commit>",
  "source": {
    "repository": "https://example.test/app.git",
    "branch": "main"
  },
  "inputs": {},
  "credentialRun": {
    "apiVersion": "doops.sh/v2",
    "kind": "CredentialRun",
    "id": "<run id>",
    "mode": "dry-run",
    "cluster": "<cluster>",
    "instance": "<instance>",
    "materializations": []
  }
}
```

Gateway 对 instruction 使用严格 JSON 解码，拒绝未知字段、target/mode/source/
workspace 不一致和敏感输入。单次授权绑定 user token、target、session、manifest、
source、mode 与完整 `CredentialRun` digest；只有在限流和 target 资源锁成功后才
消费，消费后才能转发 Ask。

## Version Gates

外层 Agent 注册必须声明 `doops.sh/credential-plan/v2`。CI/CD prompt 调用 doagent
前还必须通过 `initialize` 验证：

- `schemaVersion=doops.reconciliation-context/v2`
- `callContextSchemaVersion=doops.reconciliation-call-context/v1`
- `mcpMetaKey=doops.sh/reconciliation-context`
- `credentialRunDigest=true`
- `sourceWorkspaceCommit=true`

任一字段缺失或不同都返回 `doagent_protocol_incompatible`，不得进入模型执行。

## Secret Boundary

模型只可看到凭据 ID、grant/version ID、资源名、namespace、Secret type、key 名、
resourceVersion、digest、fingerprint、过期时间、状态和错误分类。Secret payload
不得进入 prompt、日志、Git、远端工作区或结果。

## Code Map

| Responsibility | Code |
| :--- | :--- |
| CLI entry and instruction | `backend/skills/doops-cli/cli/doops/workflow.go` |
| Declared source resolution | `backend/skills/doops-cli/cli/doops/workflow_source.go` |
| Credential prepare client | `backend/skills/doops-cli/cli/doops/credential_prepare.go` |
| Gateway prepare endpoint | `backend/agent/internal/server/credential_runtime.go` |
| Single-use prompt binding | `backend/agent/internal/server/credential_prompt_binding.go` |
| Agent/doagent dispatch | `backend/agent/internal/server/handler_ws.go` |
| CI/CD Skill | `backend/agent/skills/doops-cicd/SKILL.md` |
