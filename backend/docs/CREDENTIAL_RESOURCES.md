# DoOps Credential Resources

本文说明受管凭据资源的 CLI、权限、持久化、验证、轮换、吊销和发布引用方式。

凭据资源 ID 的互斥与操作 session 的互斥是不同边界：同一凭据的变更仍串行；不同
session 的目标执行可以并行。取消或审计收尾不得把其中一个 session 的状态扩展为
整个 target 的发布屏障。凭据使用继承 target ask/exec 权限；workload 不是独立权限维度。

## 对象模型

| 对象 | 含义 |
| :--- | :--- |
| Credential Resource | 稳定名称、personal/platform 归属、类型和资源状态 |
| Credential Version | 加密 payload 的不可变版本，保存 payload digest，不返回 payload |
| Credential Grant | 凭据的 target/use 绑定及运行位置元数据；不要求每个操作员重复授权 |
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
凭据使用继承当前用户的 target 操作权限，按 target 的 `cluster/instance` 隔离；凭据管理仍检查归属。

Web 控制台和 CLI 共享 Gateway 身份与授权模型：Web 使用 HttpOnly session cookie，CLI
使用 `doops` 的 Gateway token 缓存。两者都只能提交 credential resource name；DoSql
数据库 skill 不读取本地连接文件，也不把数据库密码放进 prompt、浏览器状态或审计文本。

```bash
doops credential list --target <target>
doops credential list --target <target> --view managed
```

- 有 target 的 `ask` 或 `exec` 权限时，默认 `target` 视图返回绑定该 target 且存在 active version
  的 active resource，不再要求给每个操作员重复授予凭据权限。
- `managed` 视图返回当前用户拥有的 personal resource；platform admin 可查看平台资源。
- `managed` 视图只返回 resource metadata，不加载 grant 详情；CLI 的 `grants=not_loaded`
  不是“没有 grant”。检查 target 可用性应使用 `credential list --target <target>`，但它同样
  只显示已有 active version 的 active grant。
- target 视图不返回 owner、creator、grantee 或 payload 等管理/秘密字段。
- 未授权详情使用 404，避免泄露资源是否存在。

## Web 控制台管理

凭据页面的 `我管理的` 视图使用 `credential list --view managed` 的资源元数据，并行读取
`GET /v1/credentials/<id>/versions` 和 `GET /v1/credentials/<id>/grants`。页面展示服务端
返回的 staged、active 和历史版本，以及所有授权的完整 metadata scope；不会从本地状态、
更新时间或 resource ID 推断版本。页面不会缓存、回显或把 payload 放入任务上下文。

切换到 `当前目标可用` 后，页面使用该 target 返回的实际 grant 元数据展示授权状态。对
active grant 的 staged version 执行验证时，页面原样提交该 grant 的完整
`cluster/instance/namespace/use/workload` 及 image、Helm 或 required keys 字段；不会
重新推断或放宽 scope。Promote 请求仍由 Gateway 检查所有 active grant 的验证状态；
grant 吊销和 resource 吊销都沿用 Gateway 的清理流程。

target 视图只读取 target 接口，不并行请求 managed 列表或 versions；管理接口的权限状态
不能清空成功返回的目标凭据。该视图仅展示可用凭据与目标绑定，管理操作在 `我管理的`
视图执行。查询接口只返回 metadata，不返回 payload。

## 开发凭据 Preflight

开发项目可在 `.agent/doops-credentials.yaml` 声明需要使用的受管凭据引用：

```yaml
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-114-k8s
credentials:
  - credentialRef: cnb-development
    use: gitCheckout
  - credentialRef: cnb-registry
    use: imagePull
```

执行检查：

```bash
doops -session dev-preflight-<task> credential preflight \
  --manifest .agent/doops-credentials.yaml
```

Given 当前用户已经登录且精确 target grant 为 active，When preflight 查询 manifest 中的
每个 `credentialRef + use`，Then 全部匹配时返回 `credential_preflight=ready`；缺失、
use 不匹配、revoked grant 或同名资源不唯一时返回非零。

安全边界：

- manifest 只能包含 `apiVersion`、`kind`、`target`、`credentials[].credentialRef`
  和 `credentials[].use`；
- unknown field 会被严格拒绝，因此 password、token、cookie、payload 和本地 secret
  路径不能混入清单；
- `credentialRef` 只接受小写字母、数字、点和连字符组成的资源名，不接受 URL、文件路径、
  token 形态或 fallback 值；
- manifest 可以进入 Git，但它只是需求清单，不创建 grant、不授予权限、不物化 secret；
- preflight 不从项目名、目录、prompt 或历史命令推断 credentialRef；
- preflight 不为本机进程或远端 workspace 注入 payload。实际使用仍走精确 grant、
  verification、promotion 和内部 materialization。

## Scope 和权限

### Resource scope

- `personal`：owner 是创建者。owner 可以写入版本、授权、验证、轮换和吊销。
- `platform`：只有 platform admin 可以创建、管理、授权、轮换和吊销。
- platform admin 需要 Gateway 全局 `admin` 或 `*` 权限，不由某个 target action 推导。

personal owner 给 target 创建绑定前必须能操作该 target。运行时使用和验证继承已有
`ask`/`exec` 权限，不再检查独立 `credential:use`；验证、轮换、吊销的资源管理权限仍检查 owner/admin。

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
| `database` | `opaque` | 目标 `cluster/instance`、物化 `namespace` 和 `use=database`；数据库资产、环境、引擎、数据库名由 DoSql/Agent 校验 |
| `helmPull` | `helmRepository` | target 上可用的 Helm 仓库凭据；实际 Chart 由任务和仓库来源决定 |
| `gitCheckout` | `gitToken` | 无；当前 scope 不包含 branch/ref |

数据库 grant 不绑定数据库资产、环境、引擎、数据库名或 workload；这些是 DoSql 的执行上下文，
DoSql 请求直接由 Gateway 读取保存的 target/database 绑定，物化使用绑定的 namespace；不调用模型生成凭据 scope。
Agent 会根据当前 target 的非 Secret discovery 校验。PostgreSQL 和 MongoDB 使用
`connection_uri`；MySQL 使用
`required_keys=SPRING_DATASOURCE_URL,SPRING_DATASOURCE_USERNAME,SPRING_DATASOURCE_PASSWORD`。
`database_asset_id` 必须来自 DoSql 的 target discovery 或授权代码资产，不能由 CLI 根据
数据库名猜测；它不再进入 Gateway grant equality。`credential grant`/`verify` 只验证
Gateway target、credential ownership、materialization namespace 和 payload usability；DoSql
query/apply 继续验证资产、引擎、数据库名、SQL policy、approval 和 evidence。

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

验证以本次请求的 Chart URL 和受管 payload 仓库 URL 为下载边界，带 HTTP Basic 读取真实 `.tgz`，
校验 gzip/tar、`Chart.yaml`、`values.yaml` 并返回包摘要，不读取 `index.yaml`。
下载 URL 必须属于 payload URL 的 HTTPS origin；仓库路径不参与权限匹配。重定向、私网地址、无效包和超限失败关闭，
不会改为匿名请求。CNB 的 payload URL 可用 `https://helm.cnb.cool`，Chart 使用其官方
`/charts/<repo>/<name>/v/<version>.tgz` 接口。当前 remote 仅支持 HTTPS `.tgz`
下载，不支持 `oci://`；调用方须明确授权 HTTPS Chart 身份，不会猜测、自动替换仓库路径或 fallback。

Helm grant 只绑定 target 的 cluster/instance 和 use=helmPull，不再把 namespace、workload、
release 或 Chart URL 写成 Gateway 授权条件。实际请求仍必须从受管凭据 payload 的仓库 URL
同源下载，并通过 Chart 内容校验；这保证凭据不会被发往无关 host，
同时允许同一 target 使用多个 namespace、release 和 Chart。

发布时 Gateway 仍只发送物化元数据。Agent 为本次授权 prompt 建立临时、无 payload 的
租约，结构化 Helm 内部匹配 session 与 credential/grant/version/resourceVersion；
namespace、release、Chart 不是授权条件。凭据物化不下载历史 Chart。实际发布下载当前
Chart 并验证后一次 staging，dry-run 与 Helm mutation 使用同一个文件。
租约在 prompt 结束时移除；模型和公开 MCP 参数不读取、提交或回显 Secret 值。
受管发布保留认证、target 权限与固定下载文件，允许明确授权的零或多个有序
`value_files` 和非敏感 Chart-specific `set_strings`，不强制 bundled-only；覆盖资产不扩大
凭据授权，未授权的远程 values 不可使用。普通升级不再隐式保留历史 values；完整迁移和
发布校验契约见 [`NATURAL_LANGUAGE_DEPLOYMENT.md`](NATURAL_LANGUAGE_DEPLOYMENT.md)。

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

## 执行结果与资源所有权

凭据物化使用已有用户、target、session 和运行位置元数据。发布内部调用继承当前执行
所有权及 actor/token，不重复获取已持有的资源锁。取消 ACK 不表示物化或发布已停止；
缺少原始终态，或外部物化/清理/发布已被中断而无法确认副作用时，保留
`outcome_unknown` 并报告失败；本地命令退出不证明远端状态。不得自动接管、
重放或扩大凭据授权，人工声明不得改写审计或强制释放活动 lease。凭据锁按资源 ID 获取；多凭据按 ID
排序取得锁，等待取消时释放已取得的锁。不同凭据可并行，同一凭据的发布、授权、
轮换和吊销保持串行，避免执行期间权限或版本被改变。
内部子调用的终态审计失败必须传播失败，不得宣称成功；执行结束及清理完成后释放 lease，
原始终态证据独立重试落盘，不用审计重试阻塞后续执行。
历史未知审计不参与新任务准入，不改为 success，也不删除；实际活动任务仍按资源锁串行。
`executionLifecycle` 是后台执行准入条件，不是凭据物化、清理或前台 apply 的总开关；
这些调用继续要求真实终态并如实报告未知结果，不因缺少后台能力而拒绝。

## 一次配置（推荐）

`credential configure` 复用本文已有的 resource/version/grant/verification/promotion，
不是另一套凭据存储或授权系统。已有资源用 `--id`；新资源用
`--name <name> --scope <personal|platform> --type <type>`，名称和归属唯一匹配时复用，
不重复创建。同一 Gateway 沿用当前登录用户；`--target` 提供精确 cluster/instance。

已有目标 Kubernetes Secret 时，无须读取并粘贴密码：

```bash
doops -session db-setup credential configure \
  --target <target> --id <existing-credential-id> \
  --namespace <namespace> --use database \
  --from-secret <source-secret> --keys connection_uri
```

如果源 Secret 只有密码字段，可传不含密码的连接事实，由 Agent 内部组合：

```bash
doops -session db-setup credential configure \
  --target <target> --id <existing-credential-id> \
  --namespace <namespace> --use database \
  --from-secret <source-secret> --keys DB_PASSWORD --password-key DB_PASSWORD \
  --connection-uri 'postgresql://<user>@<host>:5432/<database>'
```

边界：

- `--from-secret` 仅用于 `opaque`，要求 platform admin、精确 target/namespace/Secret 和
  明确的 key 集合；源 Secret namespace 与本次物化 namespace 相同，不修改源 Secret。
- URI 仅支持 postgres/postgresql/mongodb，必须含用户名、host 和数据库路径，不允许密码、
  query 或 fragment；`--password-key` 必须是唯一选中的 key，输出 payload 只有 `connection_uri`。
  需要其它连接形式时由安全输入端通过 stdin 提交完整 payload，不自动改写或降级。
- 不带 `--from-secret` 时只读 stdin，格式与 `credential put` 相同；Secret 模式不读 stdin。
  导入值经内部 Agent 控制通道进入 Gateway 加密存储，不进入公开 RPC、prompt 或 CLI 输出。
- 自动为当前用户建立精确 grant，读取并验证该资源全部 active scope 后才 promote。
  Database use 无需 workload 或数据库资产参数；其它 use 仍需原有 workload 和精确授权字段。
- 任一步失败即非零退出，不宣称就绪、不自动重试或清理已有资源。重跑会产生新的 staged version。
  该流程不是跨请求事务；失败前已完成的资源/版本/授权保留，输出仅含阶段 ID 和元数据。
- `phase=promoted` 表示受管凭据已激活；opaque/database 验证的是 Secret 物化与内容一致性，
  不是数据库登录或 SQL 执行成功。业务验收仍通过 DoSql 完成。

后续执行只引用稳定资源名：`doops -session <s> sql --target <t> --credential <name> ...`。
旧的分步命令保留给轮换、权限维护和故障诊断；Web 的版本/授权管理仍使用相同底层 API。
Secret 导入需要 Gateway 和目标 Agent 都发布此能力；旧端报错时应升级，不绕过到 shell 取密。

## 分步创建和首次激活

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

Gateway 不从 resource name 推断 scope。完整 workload/image/Helm 事实必须来自任务或
其明确授权的仓库、Chart、values、现有 release 的只读证据，并与 grant 精确一致；
不得猜测或要求将已明确资产中的事实再粘贴到 prompt；事实读取不能扩大 scope。

## 状态和可见输出

Resource：`active -> revoking -> revoked`。

Version：`staged -> active -> superseded`；resource 吊销后版本为 `revoked`。

Grant：`active -> revoking -> revoked`。

任何 CLI、Web、audit、verification 或 Agent 输出都不得出现 payload value。内部
Gateway-to-Agent 凭据控制面会把解密 payload 临时传给目标 doops-agent 物化工具；
payload 不进入公开 prompt、doagent 上下文或 metadata binding。

注意：Resource `state=active` 不等于存在 active Version。数据库执行必须同时满足：
active resource、active version、目标上的 active database 绑定和当前用户的目标操作权限，
以及该 version 在所有 active grant 上完成 verification。标准顺序是
`credential put -> credential grant -> credential verify -> credential promote`。
缺少 active version 时，target credential list 会隐藏该 resource，DoSql 执行会被 Gateway
返回 `credential_version_not_found`；缺少目标绑定返回 `credential_grant_denied`，不把缺少配置说成 `credential_invalid`。

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
