# DoOps Credential Resources

本文说明受管凭据资源的 CLI、权限、持久化、验证、轮换、吊销和发布引用方式。

## 对象模型

| 对象 | 含义 |
| :--- | :--- |
| Credential Resource | 稳定名称、personal/platform 归属、类型和资源状态 |
| Credential Version | 加密 payload 的不可变版本，保存 payload digest，不返回 payload |
| Credential Grant | 某个用户对精确 target/workload/use scope 的持久授权 |
| Credential Verification | staged 版本在某个 grant scope 上的实际物化和验证证据 |
| Credential Bundle | 一组 credential ID 的静态元数据集合，当前不参与 `ask --apply` 展开 |
| Materialization | 目标环境中的 Kubernetes Secret 或临时 Helm/Git 凭据上下文 |

Gateway 使用 SQLite 持久化元数据、加密 payload、grant、verification、materialization
inventory 和 audit。`DOOPS_GATEWAY_SECRET_KEY` 是必需的加密主密钥；Gateway 不自动生成
替代 key。

## 登录和隔离

```bash
doops login --target <target> --username <user>
```

登录态按规范化 Gateway URL 保存。同一 Gateway 下所有 configured target 共用一次登录，
但凭据查询和使用仍按当前用户及 target 的 `cluster/instance` 隔离。

```bash
doops credential list --target <target>
doops credential list --target <target> --view managed
```

- 默认 `target` 视图只返回当前用户在该 target 上拥有 active grant 的 active resource。
- `managed` 视图返回当前用户拥有的 personal resource；platform admin 可查看平台资源。
- target 视图不返回 owner、creator、grantee 或 payload 等管理/秘密字段。
- 未授权详情使用 404，避免泄露资源是否存在。

## Scope 和权限

### Resource scope

- `personal`：owner 是创建者。owner 可以写入版本、授权、验证、轮换和吊销。
- `platform`：只有 platform admin 可以创建、管理、授权、轮换和吊销。
- platform admin 需要 Gateway 全局 `admin` 或 `*` 权限，不由某个 target action 推导。

personal owner 给 target 创建 grant 前，还必须拥有该 target 的 `ask` 权限。执行
verification 还需要该 target 的 `ask` 和 `credential:use` 权限。

### Grant scope

每条 grant 精确匹配：

- `cluster`、`instance`；
- Kubernetes `namespace`；
- `use`；
- workload kind 和 name；
- image repository/digest、Helm release/chart 或 required keys。

workload kind 仅支持 `Deployment`、`StatefulSet`、`DaemonSet`。不支持 wildcard、前缀、
继承、Pod、Job 或 CronJob。grant 不绑定 session；session 只绑定一次运行和物化记录。

| use | Credential type | 额外必填字段 |
| :--- | :--- | :--- |
| `imagePull` | `registry` | image repository、64 位 `sha256:` digest |
| `tls` | `tls` | 无 |
| `opaqueSecret` | `opaque` | `required_keys`，必须与 payload key 集合完全相等 |
| `helmPull` | `helmRepository` | Helm release、`deploy/helm/...` chart |
| `gitCheckout` | `gitToken` | 无；当前 scope 不包含 branch/ref |

## Payload schema

payload 只能从 stdin 输入，最大 1 MiB，必须是单个 strict JSON；未知字段会被拒绝。

### OCI Registry

```json
{
  "server": "registry.example.com",
  "username": "deploy",
  "password": "<secret>",
  "email": "optional@example.com"
}
```

### TLS

```json
{
  "certificate": "-----BEGIN CERTIFICATE-----\n...\n",
  "privateKey": "-----BEGIN PRIVATE KEY-----\n...\n"
}
```

验证会检查证书和私钥匹配；公开输出仍只包含状态、resourceVersion、digest 或错误类别，
不等价于 CA 信任、hostname 或完整证书链验证。

### Opaque Secret

```json
{
  "data": {
    "API_TOKEN": "<secret>",
    "ENDPOINT": "https://api.example.com"
  }
}
```

key 和 value 都不能为空。

### Helm Repository

```json
{
  "url": "https://charts.example.com",
  "username": "deploy",
  "password": "<secret>"
}
```

验证读取仓库 `index.yaml`；不验证 grant 中的指定 chart 或 release 一定存在。

### Git Token

```json
{
  "url": "https://git.example.com/org/repo.git",
  "username": "deploy",
  "token": "<secret>"
}
```

验证执行 `git ls-remote --heads`；不验证特定 branch/ref。

Helm/Git URL 必须为 HTTPS、不得包含 URL userinfo/query/fragment，并默认拒绝私网、
loopback 和 link-local 目标。Registry 可以使用无 scheme 的 registry host。

## 创建和首次激活

1. 创建 resource：

```bash
doops credential create \
  --target <target> \
  --name <resource-name> \
  --scope personal \
  --type registry
```

2. 从 stdin 写入 staged version：

```bash
printf '%s' '{"server":"registry.example.com","username":"deploy","password":"..."}' |
  doops credential put --target <target> --id <credential-id>
```

输出只包含 `credential_id`、`version_id`、`state` 和 `payload_digest`。

3. 创建精确 grant：

```bash
doops credential grant \
  --target <target> \
  --id <credential-id> \
  --cluster <cluster> \
  --instance <instance> \
  --namespace <namespace> \
  --use imagePull \
  --workload-kind Deployment \
  --workload-name <workload> \
  --image-repository registry.example.com/team/app \
  --image-digest sha256:<64-hex>
```

省略 `--grantee-id` 时授权给当前登录用户。

4. 在相同 scope 验证 staged version：

```bash
doops -session credential-verify-<version> credential verify \
  --target <target> \
  --id <credential-id> \
  --version-id <version-id> \
  --cluster <cluster> \
  --instance <instance> \
  --namespace <namespace> \
  --use imagePull \
  --workload-kind Deployment \
  --workload-name <workload> \
  --image-repository registry.example.com/team/app \
  --image-digest sha256:<64-hex>
```

验证会临时物化 staged version，检查 Secret 类型/key 集合及类型特定外部能力，然后恢复
当前 active version 或清理临时资源。输出仅包含状态、resourceVersion、digest 和错误类别。

5. 激活版本：

```bash
doops credential promote \
  --target <target> \
  --id <credential-id> \
  --version-id <version-id>
```

所有 active grant 都必须存在该 staged version 的 verification。Promotion 只重新物化
当前 active grant，旧 active version 变为 `superseded`。

## 轮换

轮换使用同一 resource：

1. `credential put` 创建新的 staged version。
2. 对每个 active grant 的精确 scope 执行 `credential verify`。
3. 检查验证输出只有 metadata。
4. 执行 `credential promote`。
5. 通过 `credential list --view managed` 和目标功能路径确认新版本生效。

不要创建同名临时 resource 替代轮换，不要通过参数、环境回显或 issue 传递 payload。

## 吊销

吊销单条 grant：

```bash
doops credential grant-revoke \
  --target <target> \
  --id <credential-id> \
  --grant-id <grant-id>
```

Gateway 先清理该 grant 的全部物化上下文，再把 grant 置为 `revoked`。

吊销整个 resource：

```bash
doops credential revoke \
  --target <target> \
  --id <credential-id>
```

Gateway 按 `active -> revoking -> revoked` 清理所有物化资源。Resource revoke 是终态，
当前没有恢复、删除、改名或改类型能力。

## Bundle

Bundle 当前只是 credential ID 的静态集合：

```bash
printf '%s' '{
  "name":"release",
  "scope":"personal",
  "items":[
    {"credential_id":"cred_..."},
    {"credential_id":"cred_..."}
  ]
}' | doops credential bundle-create --target <target>

doops credential bundle-get --target <target> --name release
```

当前限制：

- `ask --apply --credential` 只接受 resource name，不接受 bundle name；
- bundle 没有 update、list、revoke、grant、verify、promote 或自动物化；
- platform bundle 仅 platform admin 可读取；
- `bundle-get` CLI 只输出 bundle 基本元数据，不输出 items。

## 发布引用

```bash
doops -session release-<commit> ask \
  --target <target> \
  --apply \
  --credential <resource-name> \
  --msg '在 <target> 的 namespace <namespace> 发布 deployment/<workload>，镜像 <repository>@sha256:<digest>；原因：<reason>；验收：Secret 类型/key、rollout、实际镜像、健康检查和功能路径通过。'
```

Gateway 不从 resource name 推断 scope。任务必须提供完整 workload/image/Helm 事实，且必须
与 grant 精确一致。

## 状态和可见输出

Resource：`active -> revoking -> revoked`。

Version：`staged -> active -> superseded`；resource 吊销后版本为 `revoked`。

Grant：`active -> revoking -> revoked`。

任何 CLI、Web、audit、verification 或 Agent 输出都不得出现 payload value。内部
Gateway-to-Agent 凭据控制面会把解密 payload 临时传给目标 doops-agent 物化工具；
payload 不进入公开 prompt、doagent 上下文或 metadata binding。

## 审计

所有 Credential mutation 在执行前创建 audit，审计不可用时 fail closed。记录包括：

- user/token ID；
- credential/version/grant ID；
- target、session 和精确 scope；
- action、时间、状态；
- resourceVersion、digest 或稳定错误类别。

审计不记录 payload。具有 `credential:audit` 或 platform admin 权限的用户可以通过
Gateway `/v1/audit` 按 target 和 credential 元数据查询；当前 CLI 尚未提供独立的
credential audit 子命令。
