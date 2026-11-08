---
name: doops-harbor-replication
scope: repository
owner: doops-maintainers
status: implementation
---

# GHCR 到 Harbor 的镜像复制

`backend/scripts/harbor-replication.py` 配置 Harbor 从 GHCR 拉取镜像；
`backend/scripts/harbor-converge.py` 对显式声明的已有策略执行有界复制和 digest 校验。
两个脚本均经 DoOps Gateway 的授权 target 执行，仓库不内置集群、客户地址或策略 ID 清单。

当前默认制品服务是 `https://ghcr.io`，DoOps Agent 仓库为
`ghcr.io/symansun/doops-agent`。其它受授权 registry 可以用 `--upstream-url` 显式指定。
这份文档描述代码契约和本地模拟边界验收，不代表已在某个集群完成配置或在线验证。

## 凭据与执行边界

脚本只接收 DoOps 受管凭据 ID，从目标集群已物化的 Secret 读取载荷，并仅在内存中使用。
密码、访问令牌和带凭据 URL 不进入参数、输出、Git 或目标工作区文件。

| 受管凭据 | 默认键名 |
| --- | --- |
| Harbor 登录 | `HARBOR_ADMIN_USERNAME`、`HARBOR_ADMIN_PASSWORD` |
| GitHub Container Registry 登录 | `GITHUB_USERNAME`、`GITHUB_TOKEN` |

Harbor 用户默认是 `admin`；实际自动化应使用拥有目标项目和复制策略所需权限的账号，
并通过 `HARBOR_ADMIN_USERNAME` 指定。Harbor 密码及 GitHub 登录两个键均不能缺失。
`harbor-replication.py` 的 `--harbor-username-key`、`--harbor-password-key`、
`--upstream-username-key` 和 `--upstream-token-key` 仅改变 Secret 键名，不接收载荷。
凭据创建、验证、提升及撤销遵循 [凭据资源](CREDENTIAL_RESOURCES.md)。

复制脚本不删除 registry、项目、策略或镜像标签，不轮换或撤销受管凭据。
收敛脚本只读取状态和触发已有策略，连 registry 或策略配置也不修改。

## 配置和一次复制

从仓库根运行时使用完整脚本路径；经 DoOps 推送到工作区后，使用实际工作区路径。
以下示例中的 target、credential ID 和 namespace 都须替换为已获授权的值：

```bash
doops -session mirror-plan exec --target <target> --cmd \
  'python3 /root/ws/mirror/backend/scripts/harbor-replication.py plan \
    --harbor-url https://harbor.example.com \
    --harbor-credential <harbor-credential-id> \
    --upstream-credential <github-credential-id> \
    --credential-namespace harbor-system \
    --project doops \
    --repository symansun/doops-agent \
    --tag-pattern "v*"'
```

`plan` 查询现有对象并报告 `create`、`reuse` 或 `update`，不发写请求。
确认本次变更范围后，以相同参数执行 `run --trigger --verify`：

- 端点名默认 `ghcr-upstream`；保存 registry URL 和认证后必须读回认证存在性。
- 项目默认创建为私有；不会创建 proxy cache 项目。
- 每个 `--repository` 建一条精确过滤的复制策略，可重复此参数。
- 策略名默认 `ghcr-<project>-<repository-basename>`；相同名称的策略按所需过滤条件复用或更新。
- 复制目的地是当前 Harbor 项目，`dest_namespace_replace_count=-1`，将源路径展平至最后一段。
- `override=false`、`deletion=false`；标签不可变规则默认使用同一个 `--tag-pattern`。
- 默认手动触发；显式 `--cron` 才配置 Harbor 定时策略。cron 格式必须由目标 Harbor 接受。
- `--trigger` 通过创建执行接口触发复制，并从 `Location` 或 JSON 响应读取正整数执行 ID。
- `--verify` 从 GHCR 标签列表驱动校验，逐个比较 manifest digest，而非仅枚举 Harbor 已有镜像。

同一组输入再次执行时，已匹配对象被复用；写入后未能读回预期状态时明确失败。
脚本不会通过重复创建对象掩盖一次配置失败。

## 显式策略清单与有界收敛

`harbor-converge.py` 必须带 `--targets-file`。JSON 文件只包含本次获准处理的策略元数据；
从目标 Harbor 回读实际策略 ID 后填写，不能照抄示例 ID：

```json
[
  {
    "policy_id": 17,
    "project": "doops",
    "repository": "symansun/doops-agent",
    "tag_pattern": "v*"
  }
]
```

`policy_id` 必须是正整数，repository 为精确 registry 路径，tag 过滤只支持精确标签或
结尾带 `*` 的前缀。清单拒绝未知字段、载荷字段、重复策略 ID 和展平后冲突的目的地。
配置必须在读取凭据和连接 Harbor 前通过解析。

```bash
doops -session mirror-converge exec --target <target> --cmd \
  'python3 /root/ws/mirror/backend/scripts/harbor-converge.py \
    --targets-file /root/ws/mirror/policies.json \
    --harbor-url https://harbor.example.com \
    --harbor-credential <harbor-credential-id> \
    --upstream-credential <github-credential-id> \
    --credential-namespace harbor-system \
    --max-rounds 8 --wait-seconds 2400 --budget-seconds 10800 --json'
```

触发前回读策略，核对项目、精确 repository、tag 过滤、启用状态、展平方式、禁止覆盖和删除，
并确认策略源端点与本次 registry URL 一致。策略 ID 指向其它对象时拒绝执行。
每轮先验证全部声明仓库，再仅触发缺少标签的策略。多个独立策略全部触发后才轮询。

停止条件：

| 观察结果 | 行为 |
| --- | --- |
| 同一轮全部标签存在且 digest 相同 | `converged=true`，退出码 0 |
| digest 不同、无法读取 digest、认证失败或执行失败 | 明确失败，停止触发 |
| 一次复制完成后缺失标签集不变 | 无进展失败，检查任务后再运行 |
| 等待预算后执行仍在进行 | 返回未完成及执行 ID，不在下一轮重复触发 |
| 达到最大轮数或整体时间预算 | `converged=false`，退出码 1 |

轮数、等待和整体预算必须为正数；单轮等待被剩余整体预算约束。
Harbor 执行未终结不等于复制失败，也不能报告成功。操作员应按输出的执行 ID 查看
`/replication/executions/<id>/tasks`，确定原因和状态后再发起下一次任务。
JSON 结果可以保存在本次审计证据目录中，不含凭据载荷。

## 校验结果与发布

`missing` 表示 GHCR 有标签但 Harbor 没有；`mismatched` 表示相同标签 digest 不同，均阻止验收。
`extra` 只说明 Harbor 保留了当前 registry 未返回的旧标签，不自动删除。
标签列表或任何 manifest digest 不可读时不能把空结果当作验证通过。

业务部署继续走 [自然语言部署](NATURAL_LANGUAGE_DEPLOYMENT.md) 的 `ask --apply` 入口，
使用此次验证过的 Harbor `repository@sha256:<digest>`。复制完成不代表部署已完成；
部署仍须回读实际 workload 版本、健康和关键用户路径，并保留回滚证据。

本地验收：

```bash
python3 -m pytest backend/test/test_harbor_replication_contract.py \
  backend/test/test_harbor_converge_contract.py -q
```

测试覆盖幂等、只读计划、认证拒绝、载荷不泄露、复制执行 ID、缺失/错误/不可读 digest、
显式清单校验、错误策略绑定、成功/失败/无进展与轮询预算退出。
在线验收需在指定 target 使用受管凭据单独执行，不从本地模拟测试推断任何集群已变更。
