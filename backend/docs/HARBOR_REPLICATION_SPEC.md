# Harbor 镜像同步配置规范（草案）

状态：草案。V1 到 V7 已在 `gw-edu-coder` 实测（见第 5 节实测记录），其余标注"未验证"的条目还没有实测，不得当作已实现行为引用。

## 1. 目标与边界

- 上游：OCI 制品仓库，由 `--upstream-url` 显式指定（示例 `https://registry.example.com`），只有它能发布新镜像。
- 下游：每个集群自己的私有 Harbor，集群内工作负载只从本集群 Harbor 拉取。
- 同步方向固定为上游 registry -> 本集群 Harbor（Pull-based）。不做集群之间互相复制；跨集群复制仍是显式、带校验的单独步骤，不在本文档范围内。
- Harbor 的复制只搬运镜像。目标选择、授权和审计仍由 DoOps Gateway 与 Agent 负责。

| 集群 | DoOps target | Registry | Harbor namespace |
|---|---|---|---|
| Oilan / Test | `gw-edu-coder` | `repo.l8ai.cn:8443` | `harbor-system` |
| 腾讯 ZYJ | `gw-zyj` | `repo.example.com` | `harbor` |

## 2. Harbor 对象

每个集群各配一套，名称一致，便于脚本化：

1. Registry 端点 `upstream`
   - 类型 `docker-registry`，URL `https://registry.example.com`，开启证书校验。
   - 凭据是上游只读 token，只在 Harbor 控制台或受管凭据流程里填写，不进命令行、文档和日志。
2. 项目：与上游路径一一对应，例如 `agentcloud`、`zhiyong`。项目设为私有；集群节点用 imagePullSecret 拉取。
3. 复制规则：**每个上游仓库一条**，命名 `mirror-<project>-<repo>`。一个项目下有多个仓库时不要用一条规则带 `*` 兜底，
   否则一次触发会串行搬完所有仓库，实测 18 个 tag 跑了 20 分钟仍未结束。

| 字段 | 值 |
|---|---|
| 方向 | Pull（源为 `upstream`） |
| 源资源过滤 name | 精确到仓库路径，例如 `example/zhiyong/education/zhiyong-*` |
| tag 过滤 | agentcloud 用 `git-*`，zhiyong 用 `sha-*`，不放行 `latest`。已验证 Harbor 支持 `*` 通配 |
| 资源类型 | image |
| 目标 namespace | 本集群项目名，`dest_namespace_replace_count` 取 `-1`（展平全部层级）。已验证：源 `example/zhiyong/k8s/nginx` 落到 `sync-test/nginx` |
| 覆盖 | 关闭。这两类 tag 不可变，已存在则跳过 |
| 触发 | `scheduled` + `0 0 * * * *`（Harbor 6 字段 cron）。发布流程仍可在推送完成后手动触发一次，见第 6 节 |
| 启用 | 是 |

4. 不可变 tag 规则：项目里对 `git-*`、`sha-*` 开启 tag immutability，仓库范围用 `**` 覆盖项目下所有仓库。
   已建：`agentcloud` 的 `git-*`（规则 id 38）、`zhiyong` 的 `sha-*`（规则 id 39）。
5. 保留策略：保留最近 N 个不可变 tag，N 由存储余量决定（未定）。

## 3. 账号与凭据

- 自动化只用机器人账号，不使用 admin。已建 `robot$doops-sync`（系统级）。
- 机器人账号的实际权限分两层，缺一层就会在运行时才失败：
  - 系统级：`registry` 的 create/read/update/delete/list，`replication` 的 create/list/read，以及
    `replication-policy` 的 create/list/read/update/delete，`project:list`。
  - 项目级：`agentcloud` 和 `zhiyong` 各给 `repository:pull`、`repository:push` 和 `repository:list`。
- 只给 `replication` 权限不够：`GET /replication/policies` 会返回 403，必须同时给
  `replication-policy`。
- 项目级只给 push/pull 时，机器人能复制但列不出仓库（`GET /projects/{p}/repositories` 返回 403），
  核对 digest 会无从下手，所以项目级还要给 `repository:list`。
- Harbor v2.14.3 没有仓库级的 tag 列表接口（`GET /projects/{p}/repositories/{r}/tags` 是 404），
  tag 从 `GET /projects/{p}/repositories/{r}/artifacts` 的每个 artifact 的 `tags[]` 里读。
- admin 密码只用于一次性建机器人账号，之后轮换。
- 机器人账号和上游 token 都注册为 DoOps 受管凭据，走 `credential configure`（创建、写入、授权、验证、提升）。载荷只经 stdin 或 `--from-secret`，不出现在命令行、对话和 Git。
- 现有受管凭据 `harbor-edu-admin` 绑定在 `gw-edu-coder` 的 `harbor-system`。配置同步策略应通过它，不应在 Pod 内直接读取 Harbor 的 admin 环境变量。
- 受管凭据现状：`harbor-edu-admin`（admin 口令）、`upstream-registry-dev`（上游只读令牌）、
  `harbor-sync-robot`（机器人名与密钥）。三者都已绑定到 `harbor-system` 并物化，
  自动化脚本从物化 Secret 读取，不从命令行取。

## 4. 发布流程（目标形态）

1. 上游构建流水线推送不可变 tag，记录 digest。
2. 发布流程调用目标集群 Harbor 的复制执行接口，触发对应规则。
3. 轮询执行状态直到成功或失败，失败则发布中止。单次传输可能超过 10 分钟，
   轮询上限要按镜像大小设置，不能用固定短超时判定失败。
4. 用 digest 校验：Harbor 中该 tag 的 digest 必须等于上游的 digest，revision 标签必须等于源提交。
5. Helm 部署使用本集群 Harbor 的 `repo@sha256:<digest>`。

复制只走 Harbor 原生复制规则；配置与收敛脚本是 `backend/scripts/harbor-replication.py` 和
`backend/scripts/harbor-converge.py`，不保留本机 pull/tag/push 的兼容路径。

发布时触发复制，规则应精确到本次要发布的那个 tag，而不是走 `git-*` 全量通配。全量通配
在实测中跑了 20 分钟仍未结束，因为每个 tag 都要按上游 registry 的实际带宽逐层拉下来。按 tag 触发
只搬一个镜像，耗时与镜像大小成正比，可预期。

触发接口是 `POST /api/v2.0/replication/executions`，请求体 `{"policy_id": <id>}`，返回 201。
**响应体是空的，执行 id 只在 `Location` 响应头里**，形如 `/api/v2.0/replication/executions/1922`，
要从里面取末段数字。只读 body 会拿不到 id，看起来像触发失败，其实执行已经建好并在跑——
重试只会多建一次重复执行。`POST /replication/policies/{id}/executions` 在 v2.14.3 上两处都不给
执行 id，不能用来判断是否已触发。

### 已知性能特征

集群到上游 registry 的下载速率约 30 KB/s 到 300 KB/s，同一节点同一时刻波动很大。64 KiB 的
range 请求秒回，1 MiB 需要 3 到 4 秒，几 MiB 的 range 会在 45 秒内超时。因此：

- 小镜像复制在 1 分钟内完成，十几 MiB 以上的镜像要按分钟计。
- 传输慢不等于卡死。判断状态要看执行的任务日志里 blob 是否在推进，不要用固定超时。
- 上游 registry 响应带 `x-ratelimit-limit: 100`，但实测未成为瓶颈。


## 5. 通过 DoOps 验证的步骤

每一步都经 Gateway 审计，凭据使用受管凭据。只读步骤先于写入步骤。

| 步骤 | 内容 | 通过标准 |
|---|---|---|
| V1 只读盘点 | Harbor 版本、已有 Registry 端点、复制规则、项目、最近执行 | 能列出，且不输出任何密码 |
| V2 连通性 | 在 Harbor 所在节点测试到 `registry.example.com` 的 HTTPS 与认证 | 返回 Registry 的 Bearer realm，不是业务网关的 401 |
| V3 创建端点 | 创建 `upstream` 并执行 Harbor 自带的健康检查 | 健康状态为 healthy |
| V4 创建规则 | 建一条测试规则，只过滤一个测试 tag | 规则创建成功，字段与第 2 节一致 |
| V5 手动执行 | 触发一次并读取执行详情 | 状态成功，目标 digest 等于源 digest |
| V6 重复执行 | 再触发一次 | 已存在的 tag 被跳过，digest 不变 |
| V7 回滚 | 删除测试规则和测试 tag | 不影响其他项目 |

V2 的结果决定方案是否成立：若集群出网到上游 registry 不通，改用经已有出网节点的代理，或保留 Job 内 `skopeo copy --all --preserve-digests`（zhiyong 已有此模式）。

### 实测记录（gw-edu-coder，Harbor v2.14.3，2026-10-29）

第一批（临时测试资源 `upstream-test` / `mirror-pull-test` / `sync-test`，已按 V7 删除）：

| 步骤 | 结果 |
|---|---|
| V1 | 通过。盘点前无任何端点和复制规则 |
| V2 | 通过。Agent 内可达 `registry.example.com`，返回 Bearer realm `https://registry.example.com/service/token` |
| V3 | 通过。端点 `upstream-test`，类型 `docker-registry`，健康状态 healthy。端点只支持 `manual`、`scheduled` 两种触发 |
| V4 | 通过。规则 `mirror-pull-test`，手动触发，不覆盖，不复制删除 |
| V5 | 通过。执行 1904 状态 Succeed，成功 1 失败 0。目标 `sync-test/nginx:1.25.3` 的 digest 与上游的 `docker-content-digest` 完全一致（`sha256:b41c95c4...35a0`） |
| V6 | 通过。执行 1905 状态 Succeed，已存在的 tag 被跳过，digest 与 `push_time` 均未变 |
| V7 | 通过。删除测试端点、规则和 `sync-test` 项目，未触及任何凭据 |

第二批（正式配置 `upstream`，保留）：

| 步骤 | 结果 |
|---|---|
| V1 | 通过。盘点时 Harbor 无端点、无规则；已有项目 `agentcloud`（id 226）、`example-org`（id 11）、`zhiyong`（id 342） |
| V3 | 通过。端点 `upstream`（id=2），basic 认证指向上游 registry，状态 healthy |
| V4 | 通过。规则改为每个上游仓库一条，命名 `mirror-<project>-<repo>`，手动触发 |
| V5 | 通过。agentcloud 执行 1906 Succeed，`agentcloud/backend` digest 与上游一致（`sha256:6f66b840...ebf9b`）；zhiyong 执行 1908 Succeed，`zhiyong/zhiyong-frontend` digest 与上游一致（`sha256:8ffb395d...dc701d`） |
| V6 | 通过。重复触发后 digest 与 `push_time` 均未变 |

最终生效的复制规则（8 条，全部 `enabled`，源 `upstream`，`dest_namespace_replace_count=-1`）：

| 规则 id | 名称 | 目标 | 源仓库 | tag 过滤 |
|---|---|---|---|---|
| 6 | `mirror-agentcloud-backend` | `agentcloud` | `l8ai/agentcloud/backend` | `git-*` |
| 7 | `mirror-agentcloud-web` | `agentcloud` | `l8ai/agentcloud/web` | `git-*` |
| 8 | `mirror-agentcloud-relay` | `agentcloud` | `l8ai/agentcloud/relay` | `git-*` |
| 9 | `mirror-zhiyong-frontend` | `zhiyong` | `example/zhiyong/education/zhiyong-frontend` | `sha-*` |
| 10 | `mirror-zhiyong-lab-api` | `zhiyong` | `example/zhiyong/education/zhiyong-lab-api` | `sha-*` |
| 11 | `mirror-zhiyong-course-api` | `zhiyong` | `example/zhiyong/education/zhiyong-course-api` | `sha-*` |
| 12 | `mirror-zhiyong-ai-api` | `zhiyong` | `example/zhiyong/education/zhiyong-ai-api` | `sha-*` |
| 13 | `mirror-zhiyong-exam-api` | `zhiyong` | `example/zhiyong/education/zhiyong-exam-api` | `sha-*` |

`l8ai/agentcloud/mobile` 和 `l8ai/agentcloud/web-admin` 在上游上没有 tag，不建规则。

不可变 tag 规则：

| 规则 id | 项目 | tag 模式 | 仓库范围 |
|---|---|---|---|
| 38 | `agentcloud` | `git-*` | `**` |
| 39 | `zhiyong` | `sha-*` | `**` |

通配过滤实测（执行 1909，agentcloud/backend 规则 tag 过滤改为 `git-*`）：

| 项 | 结果 |
|---|---|
| 匹配行为 | 生效。源侧 `l8ai/agentcloud/backend` 报 `18 item(s) in total`，Harbor 侧 `agentcloud/backend` 拿到 19 个 `git-*` tag（含更早复制的 1 个） |
| digest 校验 | 17 个能与上游对上的 tag 全部完全一致，mismatch 0 |
| 落点 | 展平为 `agentcloud/backend`，与源路径的 `l8ai/agentcloud/backend` 层级无关 |
| 耗时 | 单次执行超过 20 分钟（18 个 tag、每个十几 MiB） |
| 结论 | `git-*` 通配可用，digest 逐条可校验 |

通配过滤实测（执行 1910，zhiyong lab-api 规则 tag 过滤 `sha-*`）：

| 项 | 结果 |
|---|---|
| 匹配行为 | 生效。上游侧 `example/zhiyong/education/zhiyong-lab-api` 有 10 个 `sha-*` tag，Harbor 侧 `zhiyong/zhiyong-lab-api` 同样拿到 10 个 |
| digest 校验 | 10 个全部与上游一致，mismatch 0，no-upstream 0 |
| 结论 | `sha-*` 通配同样可用 |

`git-*` 与 `sha-*` 两类通配都已在真实执行中验证，digest 逐条可校验。

实测发现，后续脚本必须遵守：

- 上游 registry 匿名拉取返回 401，端点必须配置上游 registry 凭据。令牌只读即可，已验证对 `example/zhiyong/k8s/nginx`、`example/zhiyong/education/zhiyong-lab-api`、`l8ai/agentcloud/backend`、`l8ai/agentcloud/web`、`l8ai/agentcloud/relay` 的 token 与 tags 请求返回 200。
- `PUT /registries/{id}` 的凭据字段是扁平的 `credential_type`、`access_key`、`access_secret`。嵌套的 `{"credential":{...}}` 请求返回 200，但凭据没有写入，Harbor 不报错。每次写入后必须读回端点，确认 `access_key` 有值且 `access_secret` 已设置。
- 创建规则的请求体不能同时给出源和目标两侧的 registry 对象，否则报 `either src_registry or dest_registry should be empty and the other one shouldn't be empty`。本地 Harbor 是目标侧，只填 `dest_namespace` 和 `dest_namespace_replace_count`；源侧填 `{"id":2,"name":"upstream"}` 即可。
- 创建规则后不要用 HTTP 201 判断成功，要按名称查询规则 id 并确认大于 0，再触发。
- 触发复制用 `POST /api/v2.0/replication/executions`，请求体 `{"policy_id": <id>}`，返回 201，**响应体为空，执行 id 只在 `Location` 响应头里**（`/api/v2.0/replication/executions/<id>`）。只读 body 会拿不到 id。`POST /replication/policies/{id}/executions` 不返回执行 id，不能用它判断是否已触发。
- 上游令牌注册为受管凭据（opaque，key 为 `UPSTREAM_USERNAME`、`UPSTREAM_TOKEN`），物化到 `harbor-system` 后由 Agent 内脚本读取并写入 Harbor 端点，不经过命令行和输出。
- `doops write` 只接受相对路径（落到会话工作区），`doops exec` 的工作目录是 `/workspace`，运行脚本要写完整路径。
- 机器人账号需要系统级 `replication-policy` 权限，只给 `replication` 会让规则列表返回 403。
- **上游侧有 tag 保留策略**：`l8ai/agentcloud/backend` 的 `git-*` tag 在一天内从 88 个降到 18 个，被清理过的 tag 之后查会返回 404。Harbor 里的副本不受影响，但校验 digest 时必须选当前仍存在的 tag，否则会出现"Harbor 有、上游 404"的假不一致。
- **复制速度慢不等于卡死**：见第 4 节的性能特征。判断进度要看执行任务日志里 blob 是否推进。
- Harbor 的复制执行任务列表接口是 `GET /replication/executions/{id}/tasks`，旧路径 `/log` 在 v2.14.3 已不存在。
- 项目级保留策略接口在 v2.14.3 上是 `POST /retentions`（只允许 POST，GET 报 METHOD_NOT_ALLOWED），`GET /projects/{name}/retention` 和 `GET /projects/{name}/retentions` 都是 404。保留策略还没建。

## 6. 配置脚本 `scripts/harbor-replication.py`

第 2 节的全套对象由 `backend/scripts/harbor-replication.py` 一次建成，不靠模型推理。
输入只有两个受管凭据的 id（本集群 Harbor 与上游 registry 各一个），脚本从它们在集群内
物化的 Secret 读取取值，依次收敛 registry 端点、项目、每仓库一条的复制规则和不可变 tag 规则，
再按需触发复制并逐 tag 校验 digest。

```bash
doops -session <s> exec --target <t> --cmd 'python3 /root/harbor-replication.py plan \
  --harbor-url http://harbor-core.harbor-system.svc \
  --harbor-credential cred_17c0dfc7da9f76dd73b4c260 \
  --upstream-url https://registry.example.com \
  --upstream-credential cred_8d9021b5db3c41f2b3ae7029 \
  --credential-namespace harbor-system \
  --project agentcloud --repository l8ai/agentcloud/backend --tag-pattern "git-*"'
```

`plan` 只读，逐个对象打印 `reuse` / `create` / `update`；`run` 执行收敛，可加 `--trigger`
触发复制、`--verify` 校验 digest。其余参数见 `--help`。

### 可重复执行

同一集群第二次运行收敛到同一批对象，全部报 `reuse`，不新建、不改名、不产生重复规则。
实测：registry `upstream` id=2、项目 `agentcloud` id=226 / `zhiyong` id=342、
规则 id 6 和 10、不可变规则 id 38 全部复用，`run` 未发出任何写请求。

规则名由 `--policy-prefix`（默认 `mirror`）、项目名和源仓库最后一段拼成，并去掉与项目名重复的
前缀，因此 `example/zhiyong/education/zhiyong-lab-api` 得到 `mirror-zhiyong-lab-api`，
与第 5 节表中已有的 8 条规则名逐字一致。若改成按完整路径命名，脚本会在已有规则旁边再建一条
重复规则——这是实测中发现并修掉的缺陷。

### 不做的事

脚本不删除端点、项目、规则和 tag，也不写入、作废或轮换任何凭据。校验失败时只报告差异，
不清理任何一侧。

### 校验语义

校验以**上游**的 tag 列表为准，逐个比对 `Docker-Content-Digest`：

| 分类 | 含义 | 是否失败 |
|---|---|---|
| `matched` | 两侧 digest 一致 | 否 |
| `mismatched` | 同名 tag digest 不同 | 是 |
| `missing` | 上游有、Harbor 没有，复制还没跟上 | 是 |
| `extra` | Harbor 有、上游已清理（见第 5 节上游保留策略） | 否，只报告 |

方向很关键：早先的实现从 Harbor 侧出发反查上游，上游新发布而尚未复制的 tag 因此完全不可见。
实测中上游清掉一代 tag 并新发一代后，两侧集合零交集，`missing` 才把这个缺口暴露出来。

闭环实测（2026-10-31，两个仓库都从零交集补齐到全匹配）：

| 仓库 | 执行 | 结果 |
|---|---|---|
| `l8ai/agentcloud/backend` | 1918 + 1923 | `matched=11/11`、`mismatched=0`、`missing=0`、`extra=19`，`harbor_replication=complete` |
| `example/zhiyong/education/zhiyong-lab-api` | 1924 | `matched=11/11`、`mismatched=0`、`missing=0`、`extra=10`，`harbor_replication=complete` |

过程中 `missing` 随拷贝推进单调下降（agentcloud 11 → 2 → 0），`mismatched` 始终为 0。
一次触发只搬触发时刻源侧已有的 tag：执行 1918 启动时源侧 10 个 item，期间上游又发布 1 个，
该 tag 要到下次触发（1923）才落地。所以发布流程必须在镜像推送完成之后再触发，
不能并行。

### 并行触发

`run --trigger` 是「触发一条 → 等它结束 → 再触发下一条」的直列逻辑。按实测带宽
（30–300 KB/s，十几 MiB 的镜像按分钟计），69 个 tag 串起来要一个多小时。

多条规则互不依赖，可以并发触发，让 Harbor 自己并行跑，总耗时取决于最慢那条。
2026-10-31 实测：一次性触发 6 条规则（1925–1930），27 分钟内全部 Succeed，
无失败；agentcloud 的 `web`、`relay` 各 11/11 立即匹配。

脚本只负责把每条规则的执行 id 拿回来，不要求它们同时结束，因此并发触发不影响
`--verify` 的语义——校验以每个仓库各自的上游 tag 列表为准。

### 触发时点决定要触发几次

一次触发只搬触发时刻源侧已有的 tag，触发后上游新发布的 tag 要等下一次触发。
这不是脚本的缺陷，而是 Harbor 的触发语义。实测里 4 条 zhiyong 规则触发时源侧
各 12 个 `sha-*`，触发后上游又发布了 3 个（上游变成 15 个），校验报 `missing=3`，
再触发一轮（1931–1934）才补齐。

拷贝期间上游还可能继续发布，所以**收敛点是校验，不是触发**：上游一直有人推镜像时，
`missing` 不会自己归零。发布流程的顺序是：镜像推送完成 → 触发 → 等执行成功 →
校验 `missing=0`。校验报 `missing` 就重新触发被点名的那条规则，然后重新校验，
直到 `missing=0` 或上游停止发布。

这也是校验必须以上游 tag 列表为准的原因：只有上游驱动才能发现"刚发布的 tag 还没到"，
而 Harbor 驱动的校验永远看不见这个缺口。

### 八个仓库的实测

2026-10-31 为全部 8 条规则逐一触发并校验。`mismatched` 在每一轮、每个仓库
都是 0——凡是两侧都有的 tag，digest 从未出现过不一致。

agentcloud 三个仓库上游稳定在 11 个 `git-*`，一轮触发后即 11/11：

| 仓库 | 上游 tag | 匹配 | digest 不一致 | 缺失 | 保留 |
|---|---|---|---|---|---|
| `l8ai/agentcloud/backend` | 11 | 11 | 0 | 0 | 19 |
| `l8ai/agentcloud/web` | 11 | 11 | 0 | 0 | 0 |
| `l8ai/agentcloud/relay` | 11 | 11 | 0 | 0 | 0 |

`保留` 是 Harbor 侧留着而上游已清理的 tag 数，按第 5 节的上游保留策略，
这些是上游删掉的旧一代副本，不算故障。

zhiyong 五个仓库在实测期间上游一直在发布，`sha-*` 从 12 涨到 17，所以
`missing=0` 只在某个时刻成立，下一轮校验又会出现新缺口。这五个仓库共用同一批
`sha-*` tag 名，上游一个 commit 会同时推高五个仓库的 tag 数，补齐时五条规则
都要重新触发，漏一条就留下 `missing`——实测里漏过一轮（只触发了 9/11/12/13，
漏掉 10），`zhiyong-lab-api` 因此报了 4 个缺失，补触发 1935 后归零。

所以 zhiyong 这类活跃仓库的验收标准不是"某次校验全绿"，而是"每轮
`mismatched=0`，且 `missing` 里只剩本轮新发布的 tag"。全绿快照要靠下一节的
收敛循环反复追赶，直到某一轮里上游没有新发布。

2026-10-31 06:37 用 `harbor-converge.py` 拿到了这个快照，八个仓库在同一轮
全部 `missing=0`、`mismatched=0`：

| 仓库 | 上游 tag | 匹配 | 保留 |
|---|---|---|---|
| `l8ai/agentcloud/backend` | 11 | 11 | 19 |
| `l8ai/agentcloud/web` | 11 | 11 | 0 |
| `l8ai/agentcloud/relay` | 11 | 11 | 0 |
| `example/zhiyong/education/zhiyong-frontend` | 19 | 19 | 1 |
| `example/zhiyong/education/zhiyong-lab-api` | 19 | 19 | 10 |
| `example/zhiyong/education/zhiyong-course-api` | 19 | 19 | 0 |
| `example/zhiyong/education/zhiyong-ai-api` | 19 | 19 | 0 |
| `example/zhiyong/education/zhiyong-exam-api` | 19 | 19 | 0 |

追赶过程（执行 1925–1951，27 次，全部 Succeed、零失败）：zhiyong 的 `sha-*`
从 12 一路涨到 19，每轮校验都会撞上新发布的 tag，循环因此跑了多轮才在一轮里
全部归零。agentcloud 三个仓库上游未变动，第一轮即 11/11。

这张表是**某一时刻的快照，不是稳定状态**：7 分钟后复核，`zhiyong-ai-api` 的上游
已经是 21 个、`zhiyong-exam-api` 20 个，又出现 `missing`。再跑一轮循环（执行
1952–1956）后五个 zhiyong 仓库同时到 21/21，又一次全绿。活跃仓库上
`missing=0` 本质上是瞬时的，所以发布流程要在推完镜像后自己触发一轮并校验到
`missing=0`，而不是依赖"同步已经配好了"这个状态。

反过来说，循环每次都能收敛：两次全绿（19/19 和 21/21）之间上游涨了 2 个 tag，
循环各用一到两轮追上，`mismatched` 始终为 0。所以"追不上"不是这条链路的问题，
"追上的瞬间会被下一次发布打破"才是它的常态。

### 收敛循环：`harbor-converge.py`

手工重复"校验 → 挑出落后的规则 → 触发 → 等 → 再校验"容易漏规则（实测里就漏过
一条）。`backend/scripts/harbor-converge.py` 把这个循环固定下来：

```bash
python3 /root/ws/harborsync/harbor-converge.py \
  --harbor-credential <id> --upstream-credential <id> \
  --credential-namespace harbor-system
```

它复用 `harbor-replication.py` 的客户端和校验代码，内置 8 条规则的 project /
仓库 / tag 模式映射，每轮只触发**本轮校验报 `missing` 的那些规则**，并发触发后
统一等待。退出条件三种，各自对应不同动作：

| 退出 | 含义 | 退出码 |
|---|---|---|
| 全部 `missing=0` | 收敛完成 | 0 |
| 出现 `mismatched` | digest 不一致，再触发也修不了，立即中止 | 非 0 |
| 轮次或时间预算用尽 | 上游仍在发布，报告还差几个仓库 | 1 |

`mismatched` 直接中止是有意的：同名 tag digest 不同意味着某一侧被覆写，
多触发几轮只会掩盖问题。预算用尽不算失败，只是说明上游比复制快，
按上一节的验收标准读结果即可。

循环只触发和读取：不建、不改、不删 Harbor 对象，也不写入、作废或轮换凭据。
契约测试 `backend/test/test_harbor_converge_contract.py` 钉住这些判定——
只触发落后的规则、全匹配即停、`mismatched` 立即中止、预算用尽报 incomplete、
执行失败不静默继续，以及源码里不含任何删除或凭据写入调用。

循环会跑几十分钟到几小时，必须用 `doops exec --bg` 脱离连接运行，输出重定向到
Agent 上的文件再回读。实测用前台 `exec` 跑到第 4 轮时连接断开，返回
`execution_outcome_unknown`——按红线这不证明失败，也不证明成功，要回读
`GET /replication/executions` 的实际终态（那次 7 个执行全部 `Succeed`，
断开只发生在校验阶段）。脚本本身是可重复执行的，断开后重跑会从当前状态接着收敛，
不会重复搬已经一致的 tag。

### 定时触发：`--cron`

收敛循环解决的是"已经在追赶时怎么追得上"，但它要有人跑。定时触发解决的是
"没人盯着时怎么追上"：把策略从 `manual` 改成 `scheduled`，Harbor 自己按 cron
去拉，上游发了新 tag 会在下一个周期被搬过来。

```bash
python3 /root/ws/harbor-script/harbor-replication.py run \
  --harbor-url http://harbor-core.harbor-system.svc \
  --harbor-credential <id> --upstream-credential <id> \
  --credential-namespace harbor-system \
  --project agentcloud \
  --repository l8ai/agentcloud/backend --repository l8ai/agentcloud/web \
  --repository l8ai/agentcloud/relay \
  --tag-pattern 'git-*' --cron '0 0 * * * *'
```

`--cron` 为空时策略保持 `manual`，和脚本默认行为一致。

**Harbor 的 cron 是 6 字段，秒在最前**（`秒 分 时 日 月 周`）。5 字段的
`0 * * * *` 会被拒，实测 Harbor 返回
`invalid cron string for scheduled trigger: 0 * * * *`。每小时第 0 分 0 秒写
成 `0 0 * * * *`。

触发方式属于策略定义的一部分，所以 `policy_matches` 会比对 `trigger.type`
和 cron 字符串：策略的过滤器对但触发方式不对时，脚本会 PUT 更新而不是复用，
cron 一致时才判定为 reuse。这是幂等性的边界——同一个命令跑两次，第二次
`plan` 全部显示 reuse，不写任何对象。

2026-10-31 已把 8 条策略全部改成 `scheduled` + `0 0 * * * *`（策略 6–13），
回读确认 Harbor 侧存的就是这个值，改完再跑 `plan` 全部 reuse。

改完的下一个整点（08:00:01）Harbor 自己启动了全部 8 条策略，执行 1968–1973，
没有任何人触发。这是定时触发真正生效的证据：策略不再依赖有人跑收敛循环。
8 条同时启动意味着这批拷贝共享那条 30–300 KB/s 的跨境链路，各自要跑几分钟；
实测一轮下来 8 条全部 Succeed、零失败。

定时触发不取代手动触发：Harbor 允许对 `scheduled` 策略再手动触发一次，
所以发布流程仍然可以在推完镜像后立刻触发一轮，不必等下一个整点。
收敛循环也因此继续可用——它只是把"等下一个整点"换成了"现在就跑"。

### 凭据边界

凭据取值只在进程内存里，经 Basic 认证头发给 Harbor。没有任何命令行参数接受取值，
`--harbor-password-key` 一类参数传的是 Secret 里的 key 名；错误信息只列受管凭据的 id 和
缺失的 key 名，不含取值。契约测试 `backend/test/test_harbor_replication_contract.py`
用合成 canary 钉住这一点：31 个用例覆盖凭据缺失、key 缺失、端点不健康、端点静默丢弃凭据、
规则过滤不一致时改而不重建、重复执行不新建、digest 不一致、上游 tag 缺失、执行失败中止、
触发返回的 id 只在 Location 头里，以及 canary 不出现在任何输出或错误文本中。

## 7. 未验证与待决项

- 另一个集群（腾讯 ZYJ）到上游 registry 的出网连通性。
- 机器人账号替代 admin 的权限范围。机器人已能列规则、列端点、列仓库和 artifact，
  但"用机器人创建端点和规则"这条路径还没有完整跑过一遍，当前配置是用 admin 建的。
- Oilan 的 `values.yaml` 仍指向 `repo.example.com:8443`，与矩阵中的 `repo.l8ai.cn:8443` 不一致，需先修正再切换。
  已确认 `repo.l8ai.cn:8443` 是本集群 Harbor 的外部入口（证书 `CN=*.l8ai.cn`，`/api/v2.0/systeminfo` 返回 200），
  `repo.example.com` 从本机不可达。
- 保留策略的 N 值。上游侧已有自己的 tag 清理策略，Harbor 侧的 N 值要与它协调，避免两边都留全量历史。
