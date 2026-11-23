**[测试完成] QA-01 Skill 包基线**

结论：**DONE（发现缺陷，非阻断）**。8 个内置巡检包在容器真实运行环境下 12/12 次执行全部 rc=0、JSON 可解析、协议字段齐全、无凭据泄漏、单条体积远低于 256 KiB；§5 红线 2（同盘多挂载点不翻倍）在 loop+bind 夹具上通过。但基线存在 1 处高危待复核项、3 处中危协议/正确性缺陷、2 处低危不一致，另有 1 项跨会话接口层红线观察（属 dev-sf-01 变更集）。

## 0. 测试快照与被测对象

| 项 | 值 |
|---|---|
| 仓库 / 分支 | /Users/wwyz/Documents/l8ai/doops.sh @ main |
| HEAD | `801a73e6febbc2bb16bb47641f1b260e34fe1dbf` |
| 工作区状态 | `skills_builtin/inspect-container-runtime/scripts/inspect.sh`、`skills_builtin/inspect-vm-host/scripts/inspect.sh` 为 M（未提交）；`skills_builtin/_verify/` 为 untracked |
| 冻结快照 | `cp -R backend/agent/internal/server/skills_builtin /tmp/qa01/snap`，SHA256 `d95ada16fb513a8f915d180a60e5b346c1e6fdac74d6e2c02ca236aa7468ae45`（所有容器实测均挂载该只读冻结副本） |
| 并发说明 | skill-dev-01 正在高频改 `skills_builtin/**`（本次实测期间两个脚本被修改），故全部证据绑定上述冻结快照；被测文件后续可能已变化 |

被测 8 包：inspect-vm-host、inspect-container-runtime、inspect-k8s-cluster、inspect-app-web、inspect-package-vuln、inspect-proxy-evasion（各有 SKILL.md + scripts/inspect.sh）；inspect-database、inspect-ssh-host（只有 SKILL.md）。

验收基准：`backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md` §2（2.1–2.4）、§4.1、§4.2、§5。

## 1. 环境与镜像替代（重要：本机离线）

- 无外网：`docker pull debian:12` 失败 `failed to do request: Head https://registry-1.docker.io/v2/library/debian/manifests/12: EOF`；容器内访问 deb.debian.org 为 `Network unreachable`；本地仅 20 个镜像，无 debian:12。
- 替代 A（承担 debian:12 角色，无 python3 降级路径）：`pgvector/pgvector:pg17`＝Debian 12 bookworm、arm64 原生、无 python3。
- 替代 B（承担有 python3 的真实采集路径）：`doops-t2-agent:v0.3.21`＝Debian 12 bookworm（amd64 模拟，stderr 仅 152B 平台不匹配警告），含 `/usr/bin/python3`、`/usr/local/bin/kubectl`、`/usr/bin/dpkg-query`、`/usr/bin/curl`、`/usr/bin/ssh`，但无 ss/netstat/nginx/docker 守护进程、无 /root/.ssh。
- 工具：docker 29.4.0、shellcheck 0.11.0。
- 说明：传统 `docker run ... debian:12 sh ...` 与任务给出的 `debian:12` 夹具因离线不可执行，改用上述同为 Debian 12 的镜像；doops-t2-agent 的 entrypoint 是 do-agent，故用 `--entrypoint sh`。

## 2. 缺陷清单（按包分组）

### inspect-k8s-cluster
- **D1 · 中危 · §4.1 status 枚举违约**：无 kubectl/python3 的降级分支输出非枚举值 `skipped`（顶层与 checks 双违）。
  - 证据命令：`docker run --rm -v /tmp/qa01/snap:/w:ro pgvector/pgvector:pg17 sh /w/inspect-k8s-cluster/scripts/inspect.sh`；`grep -n skipped inspect-k8s-cluster/scripts/inspect.sh`
  - 实际输出：`{"status":"skipped","summary":"节点未配置 kubectl 或 python3，跳过集群直采",...,"checks":[{"id":"k8s.client",...,"status":"skipped",...}]}`（脚本第 5 行）
  - 期望：`status` 仅取 `pass|warning|critical`；能力缺失用 `warning`＋`detail` 说明（同包 container-runtime 当前快照已改为 warning，可对齐）。

### inspect-package-vuln
- **D2 · 中危 · §4.1 潜在枚举违约（未实机触发）**：脚本 71 行在 `upgradable is None` 时把 check 状态写成字符串 `skipped`。`upgradable` 仅在 dpkg-query+apt 分支被赋值，因此在 §2.4 明确要求覆盖的 rpm/dnf/yum/apk 节点（RHEL/CentOS、Alpine）上该分支必然进入。
  - 证据命令：`sed -n '28,71p' inspect-package-vuln/scripts/inspect.sh`；本机无对应镜像故未实机复现（离线限制，见 U7）
  - 实际代码：`{"id": "pkg.upgradable", ..., "status": up_st if upgradable is not None else "skipped", "value": ...}`
  - 期望：同样改为 `warning`，detail 保留「未缓存」语义。

### inspect-vm-host
- **D3 · 中危 · 去重规则误并导致容量欠计**：去重第 2 条按 `df` 的 Source 字符串判等，而所有 overlay 文件系统的 Source 字面量都是 `overlay`，于是任意第二个 overlay 挂载点被并入 `/`。夹具中 `/mnt/ov`（独立 loop 设备上的 overlay，st_dev=70，与 `/` 的 st_dev=62 不同）被判为「已由 / 计入」。
  - 证据命令（--privileged）：`docker run --rm --privileged --entrypoint sh -v /tmp/qa01/snap:/w:ro doops-t2-agent:v0.3.21 -c 'dd if=/dev/zero of=/tmp/img bs=1M count=64 status=none; mke2fs -q -t ext4 /tmp/img; mkdir -p /mnt/a /dev/shm/up /dev/shm/work /mnt/ov; mount -o loop /tmp/img /mnt/a; mount -t overlay overlay -o lowerdir=/mnt/a,upperdir=/dev/shm/up,workdir=/dev/shm/work /mnt/ov && echo OVERLAY_OK; stat -c "st_dev=%d %n" / /w /mnt/a /mnt/ov; sh /w/inspect-vm-host/scripts/inspect.sh'`
  - 实际输出：`mounts=[/ 910.74GiB, /w 926.35GiB, /mnt/a 0.05GiB, /mnt/ov 0.06GiB(shared=true, reason="同一 source 设备 overlay（已由 / 计入）")]`；`sum(mounts.total_gib)=1837.20` 而 `disk_total_bytes=1972616090624`＝1837.14 GiB，**欠计 0.06 GiB**
  - 期望：按底层设备/挂载身份去重（st_dev、源设备或 overlay 的 upperdir 身份），不得以 Source 文本 `overlay` 作为设备唯一键。
- **D4 · 高危（待宿主侧复核）· 疑似 §5 红线 2 未通过**：真实环境（Docker Desktop/容器内）`/`(overlay) 与 `/w`(宿主盘 bind) 被同时累加，`dedupe_reason` 均为空。
  - 证据命令：`docker run --rm --entrypoint sh -v /tmp/qa01/snap:/w:ro doops-t2-agent:v0.3.21 -c "sh /w/inspect-vm-host/scripts/inspect.sh"`；本机 `df -h /` = 926Gi
  - 实际输出：`disk_total_bytes=1972558708736`（1837.09 GiB）＝910.74＋926.35；而本机根盘容量仅 926Gi，单机出现 ≈2.02× 合计；两挂载点 `dedupe_reason` 均为空。规则 3（三元组相等且一侧为 overlay）要求 tb/ub/ab 完全相同才合并，本例 910.74 vs 926.35 不同故未命中。
  - 判定说明：容器内无法证明二者是否同一物理设备（st_dev 62 vs 48、容量不同），因此**不判为失败、判为待复核**；但该形态正是 spec 红线 2 点名的「/ 与 /root/.doops 同属一块磁盘」场景，生产 Linux + bind 宿主盘时规则 3 是否命中取决于 overlay 是否回报底层文件系统同一组 tb/ub/ab。期望：由 dev/宿主侧用同一磁盘的 bind 场景复测，并用 st_dev/挂载源身份而非字节三元组兜底。
- **D7（跨包，见 inspect-app-web）**。

### inspect-ssh-host
- **D5 · 中高危 · §2.4「真实可运行 + 受管 SSH 路径」不成立：无确定性执行通道、无能力声明**。
  - 证据命令：`cat inspect-ssh-host/SKILL.md`（frontmatter 仅 name/description，无 `entrypoint`、无 `required_mcp`，正文仅提「BatchMode=yes」未指名任何工具）；`grep -rn "doops_ssh" backend/agent/internal/server/{tool_registry.go,client_tool_call.go}`；`grep -rn "mcp_doops_k8s_read" backend/agent/internal/server/{mcp_agent.go,model_provider.go}`；`docker run --rm --entrypoint sh doops-t2-agent:v0.3.21 -c 'command -v ssh; ls /root/.ssh'`
  - 实际输出：SKILL.md 无通道名；平台唯一受管 SSH 通道 `doops_ssh` 是 `clientCallable`（tool_registry.go:45）且需 `agent.StructuredSSH`（client_tool_call.go:216），不在 AI 可见工具白名单 `["mcp_doops_k8s_read","mcp_dosql_read"]`（mcp_agent.go:210、model_provider.go:151）；运行镜像有 `/usr/bin/ssh` 但 `NO_SSH_DIR`（无密钥/known_hosts）。
  - 期望：SKILL.md 明确写出受管通道与参数契约（或提供 `scripts/inspect.sh` 走受管路径），并声明能力依赖，使 AI 驱动模式可确定性执行。

### inspect-app-web
- **D7 · 低危 · 能力声明与实现/spec 不一致**：`SKILL.md:5 required_mcp: doops`，但 §2.4 该行未要求 MCP，脚本只用 curl/openssl 类本机命令，运行中也未调用任何 MCP。
  - 证据命令：`sed -n '1,6p' inspect-app-web/SKILL.md`；`grep -n 'mcp\|curl' inspect-app-web/scripts/inspect.sh`
  - 期望：删除该声明，或给出确实依赖 doops MCP 的实现理由；否则控制台「能力依赖」展示与实际不符。

### inspect-proxy-evasion
- **D8 · 低危（信息）· 端口集合超出 spec**：脚本内置 `{1080,3128,7890,7891,8118,10808,20170,20171}`，spec §2.4 列举 7 个（不含 20171）。
  - 证据命令：`grep -n 'PROXY_PORTS' inspect-proxy-evasion/scripts/inspect.sh`
  - 判定：超集不违约，但 20171 为未文档化新增，合法服务占用该端口会误报，建议在 spec/SKILL.md 同步说明。

### 跨会话观察（不属于本任务可改文件）
- **D9 · 高危（§1.1/§2.3 红线）· builtin_strategy 双轨后门仍在**：`backend/agent/internal/server/skills_builtin_packages.go:43-112` 的 `builtinSystemSkills` 仍为 8 个内置能力携带 `DriverMode:"builtin_strategy"` 与 `StrategyID: monitorStrategy*`（vm-host:49-50、ssh-host:58-59、k8s:67-68、container:75-76、app-web:85-86、database:94、proxy:101、package:109）。
  - 证据命令：`sed -n '43,112p' backend/agent/internal/server/skills_builtin_packages.go`
  - 说明：该文件属 dev-sf-01（SF-01）变更集，本会话只读观察，未改动；建议 lead 跟踪其在 SF-01 内移除。
- **D10 · 低危 · embed 根目录内的夹具污染**：新增 untracked `skills_builtin/_verify/fixtures/{dedup_vmhost.sh,dedup_overlay_shim.sh}`。`_verify` 违反 §2.1 slug 正则 `^[a-z0-9][a-z0-9-]{0,62}$`（下划线开头），且因 `//go:embed all:skills_builtin` 会被打进 Agent 二进制（`_embedcheck` 已由 skills_builtin_packages_test.go:21-22 断言该行为）。
  - 证据命令：`git status --short backend/agent/internal/server/skills_builtin`；`find skills_builtin -maxdepth 2 -type d`
  - 期望：夹具移出 embed 根（如 `testdata/` 或包外的 `_verify` 顶层忽略目录）。

## 3. 已通过项

| # | 验收项 | 证据（命令 → 结果） |
|---|---|---|
| P1 | §2 静态语法 | `sh -n` 6/6 脚本 OK（冻结快照） |
| P2 | §2 静态 lint | `shellcheck -s sh`：vm-host、container-runtime rc=0；app-web、k8s-cluster、package-vuln、proxy-evasion rc=1 且唯一告警均为 **SC2016**（Markdown ``` 围栏内反引号被误判为单引号展开）→ 属可豁免误报，非缺陷 |
| P3 | §4.1 协议字段 | 12/12 次运行（6 脚本 × 2 镜像）rc=0，```json 块全部可被 `json.loads` 解析，均含 `status`/`summary`/`metrics[]`/`checks[]` |
| P4 | §4.1 单条体积 | 217 B（app-web 无 python3）～ 5410 B（vm-host 有 python3），最大 5.3 KiB « 256 KiB 上限 |
| P5 | 凭据字样 | 正则扫全部 12 份输出（placeholder/passwd=/secret/token/api key/PRIVATE KEY/bearer）：0 命中；唯一含「passwd」字样者是 §2.4 要求的账户基线指纹 detail `/etc/passwd:<指纹哈希>, /etc/group:<指纹哈希>`，为路径＋不可逆指纹，非凭据 |
| P6 | §5 红线 2（夹具） | loop+bind 夹具：`df -P -k /mnt/a /mnt/b` 显式列出同一 `/dev/loop0` 两行，但脚本内 `df -P -k`（无操作数）只回报一次，`disk_total_bytes` = 基线 + 57381888 B（54.7 MiB 单份 ext4）→ **未翻倍，通过** |
| P7 | §5 红线 0（降级真实性） | 无 python3 时 6/6 输出 warning/跳过并给出真实原因，`metrics` 为空、不伪造 0 值（vm-host 499 B、container-runtime 412 B、k8s 277 B、app-web 217 B、package-vuln 217 B、proxy-evasion 219 B） |
| P8 | §2.1 目录结构 | 8 个有效 slug；单包 ≤2 文件、≤12 KiB（上限 1 MiB/16 文件）；无隐藏段、无绝对路径、无 `..` |
| P9 | §2.4 能力覆盖（静态核对） | container-runtime 探测 docker/nerdctl/podman + 内存上限 check；k8s 覆盖 nodes/pods/deployments+statefulsets/pvc/Warning events；package-vuln 覆盖 dpkg-query/apt 与 rpm、apk；vm-host 覆盖 CPU/内存/磁盘/负载/uptime/sysctl/进程（zombie）/账户文件指纹；proxy-evasion 覆盖端口+环境变量+可疑进程 |
| P10 | §4.2 指标键 | vm-host 实跑输出 `cpu_percent、memory_total_bytes、memory_used_bytes、memory_percent、disk_total_bytes、disk_used_bytes、disk_percent、load_1m、uptime_seconds`，§4.2 资源指标键全覆盖 |

## 4. 未覆盖项（如实声明）

| # | 未覆盖内容 | 原因 |
|---|---|---|
| U1 | §5 红线 5：AI Skill 驱动模式与脚本直跑模式在**真实 Gateway + Agent** 上的端到端验证 | 本会话只有容器级真实运行，无已接管的 Gateway/Agent 目标；需平台侧联调，不在只读容器验证范围 |
| U2 | inspect-database 运行时验证 | AI 驱动需已绑定数据库目标与 dosql MCP；无可用目标，仅做静态核对（结论：`required_mcp: dosql` 且正文指名 `mcp_dosql_read`，符合 §2.4；无 scripts/ 属 spec 允许，非缺陷） |
| U3 | inspect-ssh-host 对真实远程主机的只读巡检 | 无安全只读的远端目标与凭据；且受管通道缺失（D5），端到端无法构造 |
| U4 | 真实（非容器）Linux 物理/虚机主机上的采集语义 | 唯一可用运行环境是容器，`/proc`、`sysctl`、`df` 均为容器命名空间视图；在真机执行需要挂载/写入等操作，超出严格只读约束 |
| U5 | dnf/yum/apk 实机路径（D2 的运行时复现） | 离线无 RHEL/Alpine 镜像可拉取，仅由代码路径推断 |
| U6 | 真实 Kubernetes 集群采集 | 无可达集群；k8s 包只跑到「kubectl 可用但 API 不可达」的 warning 分支 |
| U7 | 巡检（会话巡检）与归档能力 | 本会话（DSH subagent）无此类能力，按要求写「未覆盖」，未伪造 |
| U8 | 冻结快照之后的改动 | skill-dev-01 并发修改 skills_builtin/**，实测证据绑定 SHA256 `d95ada16…`；其后变更未覆盖 |

## 5. 复现命令

```sh
cd /Users/wwyz/Documents/l8ai/doops.sh
SRC="$PWD/backend/agent/internal/server/skills_builtin"
rm -rf /tmp/qa01 && mkdir -p /tmp/qa01/run && cp -R "$SRC" /tmp/qa01/snap
find /tmp/qa01/snap -type f | sort | xargs shasum -a 256 | shasum -a 256   # d95ada16…

# 静态
for f in /tmp/qa01/snap/inspect-*/scripts/inspect.sh; do sh -n "$f"; shellcheck -s sh "$f"; done

# 无 python3（Debian 12 替代镜像）
for s in vm-host container-runtime k8s-cluster app-web package-vuln proxy-evasion; do
  docker run --rm -v /tmp/qa01/snap:/w:ro pgvector/pgvector:pg17 sh /w/inspect-$s/scripts/inspect.sh
done

# 有 python3（Debian 12 替代镜像，需覆盖 entrypoint）
for s in vm-host container-runtime k8s-cluster app-web package-vuln proxy-evasion; do
  docker run --rm --entrypoint sh -v /tmp/qa01/snap:/w:ro doops-t2-agent:v0.3.21 -c "sh /w/inspect-$s/scripts/inspect.sh"
done

# D3 夹具（--privileged）
docker run --rm --privileged --entrypoint sh -v /tmp/qa01/snap:/w:ro doops-t2-agent:v0.3.21 -c '
  dd if=/dev/zero of=/tmp/img bs=1M count=64 status=none; mke2fs -q -t ext4 /tmp/img
  mkdir -p /mnt/a /dev/shm/up /dev/shm/work /mnt/ov
  mount -o loop /tmp/img /mnt/a
  mount -t overlay overlay -o lowerdir=/mnt/a,upperdir=/dev/shm/up,workdir=/dev/shm/work /mnt/ov
  sh /w/inspect-vm-host/scripts/inspect.sh'
```

所有容器均以 `--rm` 运行、镜像与仓库 `skills_builtin/**` 只读挂载；未执行任何写操作、未读取任何凭据；仓库内唯一写入为本报告文件。
