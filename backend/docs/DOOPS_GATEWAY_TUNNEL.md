# Doops Gateway Tunnel

`doops-gateway` 是公网控制面。内网 `doops-agent` 不暴露端口，而是主动连到
gateway；`doops` CLI 和 skill 再连接 gateway，通过 `cluster/instance` 选择目标。

## Topology

```mermaid
flowchart LR
    CLI["doops CLI / Skill"] -->|"WS /v1/rpc\nGit HTTP /v1/git\nuser token"| GW["doops-gateway"]
    AG1["doops-agent\ndoops-oilan/oilan-node"] -->|"WS /v1/agent/connect\ncluster/instance"| GW
    AG2["doops-agent\ndoops-edu/edu-coder"] -->|"WS /v1/agent/connect\ncluster/instance"| GW
    AG3["doops-agent\ndoops-jm/jm-228"] -->|"WS /v1/agent/connect\ncluster/instance"| GW
    DB["SQLite\nusers / tokens / grants / audit / targets"] <--> GW

    GW -->|"route by cluster/instance"| AG1
    GW -->|"route by cluster/instance"| AG2
    GW -->|"route by cluster/instance"| AG3
```

gateway 是统一公网控制面：

- 用户侧只连接 gateway，不直接暴露内网 agent
- agent 主动长连接注册自己
- gateway 负责鉴权、授权、审计、排队和目标路由

## 公网网站代理关键节点

第一阶段的网站代理只承载 HTTP/HTTPS 请求和流式响应，链路是：

```mermaid
sequenceDiagram
    participant DNS as DNS
    participant U as 浏览器
    participant G as doops-gateway
    participant A as 内网 doops-agent
    participant W as Agent 所在网络的 HTTP 服务
    DNS->>G: app.example.com -> gateway 公网 IP
    U->>G: Host: app.example.com /path
    G->>G: 路由表匹配 host -> cluster/instance/upstream
    G->>A: 已建立 WS 上发送 proxy/http + proxy/body
    A->>A: 校验 upstream hostname allowlist
    A->>W: 发起本地出站 HTTP 请求
    W-->>A: headers + body chunks
    A-->>G: proxy/response + proxy/body
    G-->>U: 原样返回状态、必要响应头和流式 body
```

关键节点与边界：

1. DNS 只负责把任意业务域名解析到 Gateway；域名本身不携带 Agent 身份。
2. Gateway 以请求 `Host` 查 `proxy_routes`，路由记录固定绑定一个
   `cluster/instance` 和一个 HTTP(S) `upstream`。
3. Agent 只通过主动建立的 `/v1/agent/connect` 长连接接收请求，不需要入站端口，
   因此单向网络只要求 Agent 能出站访问 Gateway。
4. Agent 在实际出站前再次执行精确 hostname allowlist；allowlist 为空时拒绝所有代理
   请求，并且不跟随重定向，避免路由被上游响应带出 allowlist。
5. Gateway 与 Agent 之间使用分帧 body，不把请求体一次性读入内存；响应收到一帧就向
   客户端 flush，支持 SSE/分块 HTTP 这类流式响应。
6. Agent 发往上游时会把 `Host` 和 `X-Forwarded-Host` 重写为路由的上游主机，
   并丢弃客户端携带的 `Forwarded`/`X-Forwarded-Host`，避免上游租户解析误把公网
   测试域名当成业务域名。
7. 第一阶段明确不支持 WebSocket Upgrade 和裸 TCP；收到 Upgrade 请求返回
   `426 Upgrade Required`，后续如需 WebSocket 必须扩展成独立的二进制/帧协议。

### 配置路由

管理员使用 `POST /v1/admin/proxy-routes` 创建或更新路由，必须携带 Gateway
管理员 token：

```json
{
  "host": "app.example.com",
  "cluster": "prod",
  "instance": "node-1",
  "upstream": "http://127.0.0.1:8080",
  "enabled": true
}
```

`GET /v1/admin/proxy-routes` 列出路由，`DELETE /v1/admin/proxy-routes?id=<id>`
删除路由。Agent 侧通过 `DOOPS_PROXY_ALLOW_HOSTS` 配置精确的上游 hostname，
例如 Helm values 中的
`proxy.allowedHosts: "127.0.0.1,internal.example.com"`。Chart 会把它注入为
`DOOPS_PROXY_ALLOW_HOSTS`；默认空值会拒绝所有上游。这个配置不是公网域名
allowlist，而是 Agent 所在网络允许访问的上游主机 allowlist。

已有自升级控制器的 Helm Agent 用 `doops upgrade`；Job 复用 Agent 已挂载的
kubeconfig，操作机不需要 kubeconfig。普通 workload 发布使用
`doops ask --apply`，也不需要本机 kubeconfig。

`doops upgrade` 仅更新镜像并保留现有 values，不修改代理 allowlist。新集群 bootstrap、
已有 Helm Agent 首次启用自升级控制器，或本节显式修改运行配置时，使用
`scripts/release-managed-agent.sh`，由外部控制器显式提供 kubeconfig。脚本不自动接管
未受 Helm 管理的 workload；已有 target 路径要求 release 已存在：

```bash
bash scripts/release-managed-agent.sh \
  --target <target> \
  --session <session> \
  --cluster <gateway-cluster> \
  --instance <gateway-instance> \
  --release <release> \
  --namespace <namespace> \
  --kubeconfig <operator-kubeconfig> \
  --proxy-allowed-hosts 127.0.0.1,internal.example.com \
  --image <repository>@sha256:<digest>
```

当前 Agent 进程不执行自身 Helm mutation。省略 allowlist 参数会保留现网值；传空字符串会显式恢复为拒绝全部。

## Managed Gateway Host

当前受管 gateway 主机信息：

- SSH 地址：`106.54.197.139`
- SSH 用户：`ubuntu`
- Gateway 服务端口：`42222`
- Gateway URL：`https://doops.l8ai.cn`
- 维护用途：发布和自恢复 `doops-gateway` 控制面，不作为普通 `doops-agent` target 使用。

维护凭据不得写入仓库。需要发布 gateway 时使用：

```bash
bash scripts/deploy-gateway.sh \
  --host 106.54.197.139 \
  --user ubuntu \
  --source-commit <verified-main-commit> \
  --verify-target gw-edu-coder \
  --verify-session gateway-release-<release>
```

第一版是 SQLite + 单活可靠部署：

- SQLite WAL 数据库：用户、token、权限、审计。
- agent 注册 gateway 不使用 token，只携带声明的 `cluster/instance`；用户操作继续由 user token 和权限控制。
- `/v1/agent/connect` 当前按设计开放注册。未来如增加 Agent 工作负载身份，
  必须作为独立协议扩展并绑定 `cluster/instance`，不得复用 Gateway user token
  或重新引入 CLI per-target token。

### TODO: Installation Registration Link

Future Agent enrollment should use a Gateway-issued installation registration
link:

- an administrator issues a short-lived, one-time link bound to one exact
  `cluster/instance`;
- the installer exchanges the link for an Agent workload identity before the
  first `/v1/agent/connect`;
- the link is consumed atomically, cannot be replayed, and never authorizes user
  RPC, Git, token administration, or any other Gateway user action;
- subsequent Agent connections prove the enrolled workload identity without
  storing or reusing a Gateway user token;
- acceptance requires expiry, wrong-target, replay, revoked-link, and
  unauthenticated-registration tests.

This TODO reserves the protocol boundary only. The current release intentionally
keeps Agent registration open and does not ship a partial enrollment fallback.
- user token 只能执行授权范围内的 action。
- gateway 转发 doops JSON-RPC 操作流量和 Git HTTP 请求，不做裸 TCP 隧道。
- `push/pull` 在标准 gateway 路径下使用 Git 语义。gateway 对外暴露 `/v1/git/<cluster>/<instance>/<session>.git/...`，再通过目标 agent 已建立的反向 WebSocket 把 Git HTTP 请求透传给 agent 本地 `/git/<session>.git/...`。
- `doops_workspace_*` 是低层文件传输工具；通用 CLI 的目录同步使用 Git HTTP
  `push/pull`。这些接口不属于发布授权协议，发布任务不得用文件同步替代明确的
  target、范围、镜像、原因和验收条件。
- 密码登录签发短期 user token，默认 24 小时过期；过期 token 会在登录时顺手清理。
- token 校验通过 token id 定位单行哈希，避免多人多 token 时全表 bcrypt 扫描。
- 同一 `cluster/instance` 按 session 与明确资源互斥；普通 Shell 使用 session mutation
  lease，不同 session 可以并行，详见 Multi-User Behavior。

## Endpoints

- `GET /health`
- `GET /v1/targets`
- `GET/POST /v1/admin/users`
- `POST /v1/admin/users/password`
- `POST /v1/admin/users/disable`
- `GET/POST/DELETE /v1/admin/grants`
- `WS /v1/agent/connect?cluster=<cluster>&instance=<instance>`
- `WS /v1/rpc?cluster=<cluster>&instance=<instance>`
- `GET/POST /v1/git/<cluster>/<instance>/<session>.git/...`

旧 gateway 草稿端点和共享 token 模式已经废弃。

## Start Gateway

用户、user token 和授权由管理员维护。Agent 部署只声明绑定的
`cluster/instance`，注册连接不使用 token。Gateway 用户接口仍严格校验 user token。
产品和 skill 的日常入口是 `doops`；gateway 内部维护命令不作为业务用户操作手册暴露。

gateway 是独立发布包，不登记为 doops target，也不通过任何 target 发布自己。

标准发布入口：

```bash
bash scripts/deploy-gateway.sh \
  --host <gateway-host> \
  --user <ssh-user> \
  --source-commit <verified-main-commit> \
  --verify-target <configured-doops-target> \
  --verify-session <session-name>
```

脚本只走 SSH/SCP，并在宿主机真实 root 下校验 DB、备份二进制、替换、重启和
校验 `/proc/<pid>/exe` hash。`--source-commit` 必须与 CNB 镜像和候选二进制的
provenance 一致；旧的本地 `bin/doops-gateway` 会在上传前拒绝。`--verify-target` 只用于复用本机 DoOps CLI 的认证检查
发布前后 target 基线，不通过 target 传输或替换 Gateway。不要用
`doops exec/push/write` 或 base64 分片部署 `doops-gateway`。

手工构建入口在仓库一级目录 `gateway/`：

```bash
bash scripts/build-gateway.sh
```

```bash
./bin/doops-gateway serve -db /var/lib/doops-gateway/gateway.db -port 42222 \
  -login-token-ttl 24h \
  -operation-timeout 30m \
  -max-concurrent-operations 64 \
  -max-concurrent-per-user 10 \
  -max-queued-per-target 10 \
  -target-queue-timeout 10m
```

Token 只打印一次，数据库里只保存哈希。

如果用户使用密码登录，gateway 会通过 `POST /v1/auth/login` 签发一个 user token；CLI 只缓存 token，不保存用户密码。

## User Management

维护用户、密码、token、授权和审计属于 gateway 管理员能力。业务用户和自动化脚本只使用 `doops` 操作 target；不要在业务文档、skill 或日常流程里直接暴露 gateway 内部维护命令。

管理员签发用户 token 使用 CLI，而不是 SSH 到 gateway 主机上手工操作数据库：

```bash
doops admin token create \
  --target <gateway-target> \
  --user <username> \
  --name <label> \
  --expires 720h
```

`--target` 用于选择本机配置里的 gateway 地址和管理员 token。管理员 token
不得通过命令行参数传入。如需把新 token 写入本机缓存，必须显式加
`--save-as <configured-target>`。

轮换旧 token：

```bash
doops admin token list --target <gateway-target> --kind user
doops admin token revoke --target <gateway-target> --id <token-id>
```

管理员能力应覆盖：

- 创建/禁用用户，设置或重置密码。
- 创建、吊销、轮换 user token。
- 普通 user token 只按精确 target 授权；`target:use` grant 自动包含标准日常操作和受管凭据使用。管理员、凭据管理和 Agent 升级仍使用独立高危权限。
- 按 `user_id`、`token_id`、`cluster`、`instance`、`action`、`session`、`status` 查询审计。
- 按保留周期清理过期审计，清理前保留可追溯导出。

当前实现有一个必须显式处理的 bootstrap 行为：`POST /v1/admin/users` 创建普通用户时
会自动添加 `cluster="*"`、`instance="*"` 的 bootstrap grant。权限判断仍然是无 grant
默认拒绝，但管理员创建流程必须在交付账号前删除这条通配 grant，再按每个目标的精确
`cluster/instance` 建立 `target:use` 授权。收窄失败时立即停用新用户，不能把半完成账号留在
启用状态。完整操作顺序见 `DOOPS_USAGE_GUIDE.md`。

说明：

- `401 Unauthorized`：先读取 JSON `code` 和 `auth_domain`。`gateway_auth_failed` /
  `auth_domain=gateway` 表示 Gateway user token 缺失、无效、过期或撤销；
  `edge_enrollment_auth_failed` / `auth_domain=edge_enrollment` 表示边缘通道 enrollment
  token 缺失、无效、过期或目标绑定不匹配。裸 `401` 不足以判断受管凭据错误。
- `403 Forbidden`：token 有效，但当前用户缺少目标 `cluster/instance/action` 的显式 grant，或缺少 gateway 管理员 `admin` 权限。
- 新 target 接入后如果 `exec/ask` 直接 403，检查该用户是否已经获得对应 target/action 的显式 grant。

## Multi-User Behavior

多人同时使用时，gateway 分别控制执行额度和资源所有权：

- 不同 target 可以并发执行。
- 同一 target 上普通 mutation 按 session 串行，不同 session 可以并行。普通 mutation
  缺少有效 session 时直接拒绝，不退回 target 级独占；只有 Agent 自升级独占 target。
- 同一客户端 WebSocket 上的多个 `tools/call` 可以并行；Agent 的每条
  `notifications/message` 必须带 `params.request_id`，Gateway 和 CLI 按请求 ID
  路由，不得用 session 名称覆盖另一个并发请求的通知。
- 普通 `exec`/`ssh`/`bg`、`ask --apply`、Kubernetes mutation 和凭据物化使用
  `target + session` lease；同 session 的 workspace 写入与 mutation 不得重叠。
  session 不是 OS 或远端资源沙箱，不同 session 不得同时修改同一 Helm release 或数据库。
- 独立 session 的 push/write/clean/pull 按实际 workspace 互斥；清理其它 workspace
  时锁定被清理的 workspace，不是调用者的 session。排队超时返回 `target queue timeout`。
- 结构化文件和工作区 read 使用共享只读通道，可并发读取，也可查看正在执行的 session；
  目标 read 权限和会话路径边界仍逐次校验，Agent 自升级期间仍禁止读取。
- 结构化 K8s 的 `get/describe/logs/top/events/rollout-status`、任务状态和节点信息
  可与 target mutation 并行，结果未知时仍可诊断；Agent 自升级阻止这些查询。
- 公网 HTTP 代理只有 `GET/HEAD/OPTIONS/TRACE` 按只读查询处理；`POST/PUT/PATCH/DELETE`
  等其它方法按精确代理 route 互斥，不阻塞其它 route 或普通 session。转发中断且
  缺少终态时保留 `outcome_unknown`，不宣称业务没有执行或自动重放。
  上游收到写请求后的连接/响应体传输失败同样是未知结果；不能把代理返回的传输
  错误当成业务没有执行。下游写失败必须等待取消结果后完成审计。
- `-max-concurrent-per-user` 控制单个 gateway 用户最多占用多少个运行中操作槽；
  `serve` 默认 10，等待资源不计入执行额度，超过时返回 `user operation limit exceeded`。
- `-operation-timeout` 控制单个转发操作的最长运行时间；超时请求取消并等待原始终态。
  取消 ACK 不是终态；缺失终态时持久化 `outcome_unknown` 后释放本次 lease，不自动重放。
  历史未知审计不参与准入。取消中或审计收尾中的活跃操作只阻挡同 session/资源的冲突请求；
  审计持久化失败保留该 lease 并自动重试，不扩展为整台 target 的屏障。
- `serve` 默认全局 64 个执行槽，每个互斥槽最多 10 个等待者、等待上限 10 分钟。
  workspace 锁和 session mutation 都使用有界等待队列；同 session 冲突在允许排队时会等待
  reader/resource 终态，不占用户或全局运行配额，只有队列满、排队关闭或超时才返回
  `target busy`/`target queue timeout`。`-max-queued-per-target` 设为负数可关闭排队。
- `-login-token-ttl` 控制密码登录生成 token 的有效期；长期自动化应使用管理员发放的可轮换服务 token。

管理员先核对所有权；明确获准中断对应任务后才请求取消，不自动抢占其它发布：

```bash
doops -session <s> admin operations list --target <gateway-target>
doops -session <s> admin operations cancel --target <gateway-target> --id <operation-id>
```

Gateway 把 `cluster`、`instance` 作为管理操作查询和取消的强制 target 边界；
缺少或不匹配 target 时不会取消操作。

`cancel` 返回 HTTP `202 cancel_requested`，只确认请求已登记。原始操作的资源所有权
保留到 terminal response；取消 ACK 不是终态。shell/SSH/bg 等本地命令等待进程组
终止，`ask` 发起 doagent `session/cancel` 并等待匹配的 `turn_finished`。
`executionLifecycle` 能力只在后台 `doops_bg` 的派发前校验，要求完整终态、受控任务寿命
和取消能力。普通前台执行不要求此声明；超时/取消仍等待原始终态，不能因为缺少声明
而宣告停止或跳过未知结果保护。后台任务在断连时取消，不作为可脱离连接运行的任务队列。
本地进程退出不证明远端控制器或脱离进程组的工作停止。Shell/SSH/bg、apply、K8s
写操作等被中断时，即使收到本地取消终态也记录 `outcome_unknown`；缺失终态同样
阻止后续目标 mutation。该阻塞在 Gateway 重启和
Agent 重连后继续有效。人工声明不能改写审计或解除执行守卫；历史记录仍可读取。
已验证的 Helm 工具失败属于明确失败，不是未知结果，按正常终态释放运行锁。
受管凭据只锁本次使用的资源 ID，不再跨不同凭据串行化整个 Gateway。
`unlock` 只重连空闲且没有未决执行的目标，不自动接管或重放任务。

workspace 写入、清理和 clone/commit 缺失终态时同样保留未知结果；真实本地终态
可以证明这些本地操作已经结束，不能将其与缺失终态混淆。客户端 WebSocket 使用
已有的 60 秒写截止时间，超时关闭连接并进入取消/终态处理，不无限占用资源 lease。

结构化 SSH 收到 Agent 的终态工具结果时，远端退出状态 `1..254` 或没有退出码的
结构化工具错误都按普通失败处理，不产生未知结果隔离。退出码 `255`、信号终止、
取消、断连或缺少结构化终态仍按未知结果处理；不得解析输出文本或按命令关键字推断
终态。

Git HTTP 的 `init`、`upload-pack`、`receive-pack` 及其 hook 也由请求拥有进程组。
请求取消、输入/输出复制失败时先终止并回收进程组，再发原始请求终态；不能以
handler 返回、取消 ACK 或已经发出的 HTTP 200 作为成功依据。流式响应开始后
Git 退出失败仍返回 `execution_outcome_unknown`，不会发送成功 EOF。取消仍保留
未知结果保护，因为 hook 可能已提交部分工作区变更或触发外部操作；本地进程停止
不等于这些副作用已回滚。

`ask` 链路是 `CLI -> gateway -> doops-agent -> doagent ACP HTTP`。如果 `exec` 成功而 `ask` 失败，优先检查目标 agent 容器内：

```bash
ps -ef | grep 'do-agent acp-http'
grep -n 'apiKey\|baseURL\|model' /tmp/doops-doagent/settings.json
```

`/tmp/doops-doagent/settings.json` 是由挂载的 `/opt/doagent_config/settings.json`
物化出的可写运行时配置，必须有非空 `provider.openai.options.apiKey`；否则
doagent 会拒绝启动，`ask` 会失败。

## Start Private Agent

优先用容器或 K8s 部署。裸二进制 `/usr/local/bin/doops-agent -port ...` 只适合临时自举/排障；没有 `-gateway-url` 的进程不会自动出现在 gateway。

### 容器或 K8s 方式

```bash
docker run -d --name doops-agent \
  --privileged --pid=host --network=host \
  --restart=unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /root/.kube:/root/.kube:ro \
  -v /etc/kubernetes:/etc/kubernetes:ro \
  docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest-from-successful-CI> \
  -port 42222 \
  -gateway-url https://gateway.example.com \
  -cluster prod \
  -instance master-1 \
```

```bash
doops-agent \
  -gateway-url https://gateway.example.com \
  -cluster prod \
  -instance master-1 \
```

如果省略 `-instance`，agent 使用主机名。agent 断线后会自动重连；gateway
不会重放已中断的高危操作。任何中断的 mutation 都按失败处理，调用方必须检查真实终态，
不得由 Gateway 重新发送 apply 请求。
`-gateway-url` 必须填写 doops-gateway 的 base URL，或显式填写 `/v1/agent/connect`；
其它 WebSocket 路径都会被拒绝。Agent 不再暴露客户端直连 WebSocket 端点。

### 裸二进制自举/排障方式

如果当前机器只有 `/usr/local/bin/doops-agent -port 42222 ...` 这样的裸进程，而没有 Pod/容器，就说明还不是容器化安装。此时：

- 标准做法是重新用 `-gateway-url ... -cluster ... -instance ...` 启动 agent，或迁移为容器/K8s 安装
- 进程和健康端点只能用于本机运行状态检查；所有远程操作仍必须经过 Gateway

## Configure CLI

```json
{
  "servers": [
    {
      "name": "prod-master",
      "aliases": ["prod", "master"],
      "gateway": "https://gateway.example.com",
      "cluster": "prod",
      "instance": "master-1",
      "use": "prod cluster master via gateway"
    }
  ]
}
```

Target config stores routing only. Run `doops login --target prod-master` once;
the resulting Gateway login state is shared by every target using the same
Gateway URL.

或者：

```bash
doops add \
  --name prod-master \
  --aliases prod,master \
  --gateway https://gateway.example.com \
  --cluster prod \
  --instance master-1

doops login --target prod-master
```

## Use

```bash
doops targets --target prod-master
doops -session smoke exec --target prod --cmd 'hostname'
doops -session smoke exec --target prod-master --cmd 'hostname && kubectl get nodes'
doops -session smoke ask --target prod-master --msg '只读巡检集群节点状态'
doops -session build push --target prod-master --src .
```

## Actions

授权 action：

- `exec`
- `ask`
- `read`
- `write`
- `push`
- `pull`
- `info`
- `check`
- `clean`
- `agent:upgrade`
- `targets:list`
- `admin`

普通 user token 默认不拥有 target 操作能力。每个 target/action 都必须显式授权；
`targets:list` 只允许列出可见 target，不隐式授予 `exec`、`ask`、`write` 或其他操作。
`admin` 只用于 gateway 管理，不随默认 target 权限下发。

## Agent Upgrade

Agent 进程不执行承载自身的 Helm mutation。直接在 Agent 里跑 `kubectl set image` 或
`helm upgrade` 会在滚动更新时把回滚方一起杀掉，新 Pod 启动失败后目标可能永久离线。

`doops upgrade` 让 Agent 创建一个独立 Job。Job 复用 Agent 已挂载的 kubeconfig，
按 Chart 的 `maxSurge=1`、`maxUnavailable=0` 拉起新 Pod，并在新 Pod 未持续就绪时回滚。
镜像可以是 `repository:tag` 或 `repository@sha256:<64 hex>`。tag 使用 `Always` 拉取，
并校验 Deployment 镜像规格、Pod Ready 和实际 `imageID`；`--dry-run` 只预演。历史挂起的自升级 operation
会在 Gateway 启动时立即标记失败，不会继续占槽等待 replacement。

前置条件是在线 Agent 已包含控制器、标准 Helm Deployment 和精确 Gateway 身份，
并有可供 Job 共享的 `/root/.kube` 或 `/root/.kube/config` volume，以及所需集群权限。
缺少这些条件是目标运行配置问题，不得把普通业务发布引导成寻找操作机 kubeconfig。
CLI 必须显式传 `-session`；Job accepted 仅代表已提交，不代表升级成功。只有 Job 完成、
最终 Deployment 验证和协议探针通过后才能报告成功。

```bash
doops -session agent-upgrade upgrade \
  --target prod-master \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest> \
  --dry-run

doops -session agent-upgrade upgrade \
  --target prod-master \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest>
```

## Reliability Notes

- SQLite 数据库建议放持久卷：`/var/lib/doops-gateway/gateway.db`。
- 单活部署即可，K8s 使用单副本 Deployment 或 systemd 托管。
- 源码入口是仓库一级目录 `gateway/`；`agent/cmd/gateway` 仅保留为兼容 wrapper。
- gateway 默认监听 `42222`；公网部署时云安全组/防火墙也必须放行 TCP 42222。
- gateway 按 `cluster/instance` 和资源键隔离操作；独立结构化 workspace 可并发。
  不同 target 指向同一 release/数据库时尚无统一所有权或执行器 fencing；不得用另一
  target 绕过已有锁，也不能将当前实现描述为安全抢占或分布式 release 锁。
- 发布通过 `doops ask --apply` 的明确自然语言任务进入 Gateway。任务必须给出
  target、服务范围、镜像版本、原因和验收条件；`--credential <resource-name>` 只传递
  metadata-only 引用。Gateway 将引用绑定用户、target 和 session，并在精确 workload、
  image 或 Helm scope 获授权后物化。公开 prompt、doagent 上下文和 metadata binding
  不接收凭据 payload；Gateway 仅通过内部物化调用向目标 Agent 短暂传递 payload。
- `/v1/targets` 中 `busy=true` 只表示 target-wide 阻塞；普通 session 运行显示为 `status=active`、`busy=false`，并在 `resources` 中标出锁住的资源。
- gateway `serve` 默认全局 64、单用户 10 个执行槽；嵌入式 `NewGatewayHub` 的零值
  options 是全局 64、单用户 8、无等待队列，测试默认不代表生产启动参数。
- gateway 会主动对 agent WebSocket 发 ping，agent pong 后刷新租约和 SQLite `last_seen`。
- agent 上下线状态写入 SQLite，在线目标由内存连接表提供，断线时立即标记 offline。
- agent 重连替换旧连接时，旧连接的关闭不会把新连接错误标记为 offline。
- `ask --apply` 发生客户端或隧道断连时，当前等待路径取消并保留真实审计状态。系统没有
  CICDReport 恢复或 mutation 重放协议；实际状态不确定时必须返回 outcome unknown，并由
  操作者核查目标资源。
- 审计记录包含 user、token、target、action、session、命令摘要、状态、错误和输出尾部；输出尾部使用有界缓冲，默认只保留 8 KiB 尾部摘要。
- gateway 只转发操作流量和 Git HTTP 流量，不保存仓库快照。大文件、可恢复续传、跨用户长期共享 artifact 建议新增独立 artifact store，不把 gateway 扩展成对象存储。

## 2026-05-11 Production Smoke

这段 smoke 使用的是受控实验 gateway。doops 支持 `http` / `https` gateway；公网生产环境建议改用 `https` / `wss` gateway，但 CLI 和 agent 不会因为 gateway 是 HTTP 而拒绝连接。

这段记录对应早期受控实验环境，不是当前 target 或发布配置。实验时接入：

- `doops-114/vm-114`
- `doops-jm/jm-228`

已验证：

- `doops targets --target <historical-test-target>`
- 89/114/JM 的 `exec`
- 89/114/JM 的 `write` + `read`
- 89/114/JM 的 gateway `push` 到 `/root/ws/$SESSION`，并校验 `.doops-ready` 和文件 sha256；传输路径为 `/v1/git/<cluster>/<instance>/<session>.git` 的 Git HTTP 反向隧道
- 未授权 token 执行 `write` 返回 `forbidden` 并写入 audit
- 超过 90 秒后重复 `targets` 和 `exec`，确认 heartbeat 不误踢 agent

JM 第三实例已通过 89 内网路径接入，目标标识为 `doops-jm/jm-228`。89/114/JM 的运行容器命名已统一为标准 `doops-agent`。

## Current Managed Targets

当前文档维护口径只使用 Gateway 的 `cluster/instance` 身份，不维护推断或示例 IP：

- `doops-oilan / oilan-node`，oilan 集群，标准 k3s 部署
- `doops-edu / edu-coder`，edu-coder 集群
- `doops-jm / jm-228` -> JM 集群
- `doops-zheyin / node-239` -> 浙音集群
- `doops-114 / vm-114` -> 114 集群
- `doops-235 / hdu-235` -> 杭电节点

注意：

- `oilan` 和 `edu-coder` 是两套不同集群，不能混写成同一个 cluster
- 历史上的 `doops-89/master-node` 是旧链路名，不再作为当前标准纳管名称

## Cloud Firewall Note

如果 gateway 进程在机器本机 `127.0.0.1:42222` 健康，但从公网访问 `http://<gateway-ip>:42222/health`
超时，通常是云安全组没有放行 TCP 42222。系统防火墙放行不能替代云安全组放行。

在安全组尚未开放前，可以临时让内网 agent 经 SSH local forward 连接到 gateway 本机端口：

```bash
ssh -N \
  -L 127.0.0.1:32223:localhost:42222 \
  ubuntu@<gateway-ip>

doops-agent \
  -gateway-url http://127.0.0.1:32223 \
  -cluster prod \
  -instance master-1 \
```

这只是内网 agent 到 gateway 的临时通路；CLI/skill 作为公网入口使用时，仍应开放 gateway 公网 TCP 42222。
