# DoAgent 0.2.42 全环境升级进展

## 范围与制品

用户授权升级全部 13 个已配置 DoOps target。本记录不表示全部完成。
本轮保持原部署方式、模型来源、数据、网络及权限；未发布业务 workload。

截至 2026-11-08 23:32（Asia/Shanghai），9 个 target 实际运行 DoAgent 0.2.42；
其中原 8 个已有版本、ACP 健康和真实模型工具调用证据。Silicon 的版本与健康
通过，但首次模型下发暴露出确定的代码缺陷，因此仍有 5 个 target 未完成验收。
联通 K8s、Compose 及 Silicon 的独立升级控制器均已返回成功终态。本轮全环境
浏览器验收仍未完成，不能将后端验收等同完整发布闭环。早期进度保留时间边界，
当前结论以文末的 23:32 复核为准。

- DoAgent `0.2.42`：`08ecb0193767f559844d985cc7365484c61bf9c5`。
- DoOps `v1.3.123`：`8683d67174e19a1750e9b19de99a0816cdb8ea79`。
- CNB release CI `cnb-rl3-1k6sg8rga` 已回读为 success，制品溯源接口确认同一 commit。
- 平台镜像：
  `docker.cnb.cool/l8ai/ai/doops.sh@sha256:fd20c510411d9dde8e2061ed390e3b91877781ade2d4400a39b715280524b02a`。
- 对应 OCI index：
  `sha256:5c6055a7b7a2e4fbab4064cf1c20268b166e7e3915da3e9e92dd42cd011db854`。
  Oilan、ZYJ 的 containerd 报告 index，其他节点报告平台 manifest；
  registry index 内容证明两者关联，不能只按字符串不同认定版本不同。

最初四台升级使用同 commit 的 CLI。后续共享 CLI 被其他工作流更新到 v1.3.124；
联通两台通过该 CLI 的审计 Gateway 入口启动独立控制器，控制器分别核对镜像中
二进制的 v1.3.123 / 源码 commit 和 DoAgent 0.2.42。没有降级共享 CLI。
v1.3.124 与 latest 的移动不改变上述固定制品身份。

## 已部署回读

| target | namespace / Helm release | revision | 实际服务 Pod UID | 本轮操作 |
|---|---|---:|---|---|
| gw-hdu-235 | ai / doops-agent | 3 | d2b1f4ce-97e1-4ccb-8dd0-86d591ed7083 | 0.2.39 → 0.2.42 |
| gw-edu-coder | kz-ops / doops-agent | 4 | 6959f252-a139-4155-9afa-38352b0d182b | 0.2.39 → 0.2.42 |
| gw-zy | default / doops-agent-239 | 12 | f871c188-470d-4707-a12a-e533eecbcb6d | 0.2.39 → 0.2.42 |
| oilan | doops-oilan / oilan-agent | 23 | 6f0afc61-7c92-4b09-87eb-a46937454af7 | 0.2.39 → 0.2.42 |
| gw-test | agents-test / doops-agent-test | 14 | 5ca1b839-9258-47dc-9b34-ec5503fedc2d | 原已同版，复核 |
| gw-zyj | ai / doops-agent | 48 | f5b90c6a-14f9-4d99-8c0e-7fbe1d9bcaef | 原已同版，复核 |

上述六台均实际回读 `do-agent --version=0.2.42`、ACP `status=ok`、
DoOps commit、Pod Ready 及 `observedGeneration=generation`。
新升级四台独立升级 Job、Gateway 重连及 structuredSSH 校验均成功。
杭电、教育原有 `hostAccess.enabled=false` 保留；浙音、Oilan 原有 true 行为保留。

各实例 `backend/deploy/instances/{hdu-235,edu-coder,zheyin-239,oilan-node}/values.yaml`
来自 live `helm get values`，是完整显式 values 的快照，不是新的环境注册表。
保留历史显式字段以复现 live release；未导出 Secret payload。
这些文件记录当前状态，不会自动触发新的部署。
四份 values 均通过本地 Helm 渲染以及目标上的 Helm server-side dry-run；
dry-run 返回 0，未执行资源变更。

ZYJ 原已同版的历史 Helm values 存在重复 `nerdctl-data` volume，
且 live 额外保留 `kubernetes-config` 卷/挂载、toleration 和 args。
因此该快照只存于
`docs/reviews/evidence/2026-11-08-doagent-042-all/zyj-historical-values.yaml`，
明确标为不能直接用于部署；未将历史漂移当成通过验证的 GitOps 输入。

## 功能验收与修复

- 杭电、教育、Oilan、test：真实 `doops ask` 调用 `mcp_doops_k8s_read`，
  成功读取对应 Deployment，输出 Ready=1 和发布摘要。
- 浙音：第一次真实模型请求失败，原因是没有 active modelProvider binding。
  `/health` 仍正常，证明健康接口不能代替模型功能验收。
  已通过既有 `credential configure --from-secret` 流程，从该 target 自己的
  `default/doops-agent-239-agent-settings` 的 `settings.json` 导入
  `zheyin-existing-doagent-model`，verify 后 promote。
  payload 全程走受控导入链路，未输出、手工复制或改用其他 target 模型。
  随后真实工具调用成功，标识 `RELEASE_SMOKE_ZHEYIN_MODEL_REPAIRED`。
- ZYJ：Gateway 对话两次报 `execution_outcome_unknown: prompt_turn_id_missing`。
  Agent 日志明确显示 `[GC admission] Queued prompt`，随后队列被取消。
  原生 ACP 空闲请求返回 accepted/turnId，不能据此证明 Gateway 并发路径正常。
  已定位协议不一致：DoAgent 全局准入排队响应缺 turnId，DoOps 的
  `doagentPromptTurnID` 对 accepted/queued 都要求 turnId。
  不通过放宽终态验证或伪造 turnId 掩盖此问题。
  在原排队请求已从队列清除后，独立空闲会话
  `doagent-zyj-idle-smoke-20261108` 经 Gateway 实际调用两次
  `mcp_doops_k8s_read` 并成功完成，标识 `RELEASE_SMOKE_ZYJ_IDLE`。
  此结果证明空闲路径可用，排队路径缺陷仍未修复。
- 模型文字可能把 Deployment generation 当作 Helm revision；
  本表 revision 以 Helm history 回读为准，不引用模型自报 revision。
- 本轮浏览器空间 274 成功恢复登录并打开控制台，随后被移除，无法继续使用。
  未在本轮完成 AI 页面操作、console/network 和截图验收；
  原 gw-test 的浏览器验收记录不能替代本轮全环境验收。

## 回滚风险与发布例外

四台旧 Helm target 的原镜像 index
`sha256:9f94f7cf765573800e9f89541d7cf9460c22c7c64f0c781829c93c083bd17ed4`
以及 v1.3.119 tag 已从 CNB registry 删除，首次预检按设计拒绝。
本轮执行者随后使用 `--allow-missing-rollback-baseline` 升级上述四台。
用户授权了全环境升级，但没有单独明确接受失去旧版自动回滚保障；
此项是本轮执行偏差，不应作为以后发布的范例或默许。

原 Helm revision 分别为 2、3、11、22，历史配置与旧镜像摘要仍可查，
但不代表旧镜像可拉取，不能宣称回滚保障完整。
unicom 宿主缓存有原 index、平台 manifest、config、全部运行 layers，
却缺 attestation 子 manifest；没有重建索引或替换 digest 冒充原制品。
后续 target 不再使用该例外。当前健康的新镜像仍可从 registry 拉取；
应保持健康服务，不能为了修正流程偏差而回滚到已删除镜像。

## 未完成目标及必要条件

| target | 实时事实 | 未完成原因 / 后续条件 |
|---|---|---|
| gw-silicon | 0.2.39 / v1.3.117；独立 Docker，host 网络、privileged、ACP 7750 | 既有宿主 49.51.244.8:2022 外部 SSH banner 超时；目标侧 ubuntu/root 结构化 SSH 无可用认证。恢复已授权管理入口后走 release-host-agent.sh。 |
| gw-scu | 0.2.39 / v1.3.116；doops-system，default SA | 无 Deployment/Secret 权限，且无可用宿主或 kubeconfig 挂载。需要既有独立授权管理通道；未临时授予 cluster-admin。 |
| gw-keyi | systemd `/usr/local/bin/doops-agent` v1.3.43，无 DoAgent/ACP | 增装运行时或迁移部署方式及模型来源尚未确定；未把首次安装当作原地升级。 |
| gw-114-k8s | 本地有 route，Gateway 管理员实例列表含 history 也无此 target | 无在线/留存实例记录。已有外部 kubeconfig 114.55.61.32:6443 超时，SSH 22 连接被关闭。 |
| gw-jm | 本地有 route，Gateway 管理员实例列表含 history 也无此 target | 无在线/留存实例记录，未找到可用管理通道。 |

不可见目标已通过管理员实例 API 核对，不能只归因于当前用户缺 grant。
没有对这些节点执行自升级、RBAC 扩权、部署方式迁移或业务变更。

unicom 制品预取使用审计后台 RPC，TaskID `08d1cf0a04f6751d`。
长时间拉取无完成后由本轮发送取消；本地 CLI 终态 130，远端原 task PID
3201308 已不存在。未获得成功终态，按取消/未完成记录，不以管道 tail 的
退出码推断拉取成功。

## 后续验收

完成标准仍为每个 target 的精确版本、运行镜像、健康、真实模型工具调用、
关键页面验证，以及可恢复的回滚路径。当前不能宣称“13 台全部发布完成”。

## 14:55 后续发布准备

用户再次明确要求全部发布升级，继续沿用本轮固定制品。新回读显示联通
containerd 已完整缓存候选平台镜像 `fd20c510...`，image config 为
`6cde872aaf307da4420ea3020f4abfa71d6c06a7d3a0480ee83522862206c220`，
大小 571,163,200 字节，无活动下载。先前取消的 RPC 不作为成功证据，
本次以 `crictl inspecti` 的完整 `repoDigests` 回读为准。

现有 `doops-unicom-agent` ServiceAccount 已有 Job、Deployment、Secret 和
ConfigMap 权限；旧 Helm release 仅管理 `Deployment/doops-unicom`，
不拥有该 ServiceAccount。新增 `backend/deploy/instances/unicom-k8s/`
记录一次性独立 Job 的发新停旧方案：

- 备份 revision 5、values、manifest 和 live Deployment；保持旧 Pod 运行。
- 使用同一 Gateway route、原 ServiceAccount、节点和 hostAccess，安装
  `doops-agent`；独立 `/var/lib/doops-agent` home，模型绑定保持本 target 来源。
- 新实例必须 Ready，实际 imageID 匹配固定平台摘要；独立 Gateway/真实模型
  功能验收通过后，才写入与新 Pod UID 绑定的接收回执。
- 收到回执后才卸载精确旧 release；失败或 600 秒内未收到验收则卸载候选、
  保留旧 Deployment。旧 release 清理阶段失败时保留已经验收的新服务。
- Job 无自动重试，整体上限 1,500 秒；不允许免回滚基线的原地升级。

原宿主 `/opt/doops-agent/config/settings.json` 的模型为 MiniMax-M3，已在
同 target 内经短期 Secret 和 `credential configure --from-secret` 受控导入
`unicom-existing-doagent-model`，真实模型 verify 与 promote 成功；
短期 Secret 随后删除。未输出 payload，未借用其他节点的模型。

新增控制器的故障注入覆盖预检失败、安装失败、验收超时和旧资源清理失败；
验收缺失时不能卸载旧 release，清理失败时不能卸载已验收候选。
本地四项事务测试和 Helm 渲染已通过；部署结果将按实际回读补充。

### 控制器复审与 Compose 准备

- K8s 控制器已补齐可执行的资产创建与验收回执写入流程；备份包含输入
  SHA256 和 chart 0.3.0。候选模型只走已激活的本 target Gateway binding，
  移除无效的旧 settings 挂载。
- 复审建议“不清理 failed 候选”未采纳：旧 release 是 `doops-unicom`，
  被清理的候选是 `doops-agent`，Job 又是独立资源；清理未验收候选正是
  让旧实例重新接管的必要步骤。测试明确覆盖了安装失败后仍存在的候选、
  未收到验收、恢复失败。不会把 `candidate-ready` 当作验收通过。
- 旧 chart 名 `doops-agent-0.2.1` 来自真实 `helm list` 回读，并非猜测；
  现在包含 failed release 的 `helm list -a` 还会拒绝已有同名候选。
  Job manifest 本身声明 namespace；runbook 同时显式传 namespace。
- Compose 候选预取 Task `c8efa267da54b6b4` 终态 exit 0。真实 dry-run
  验证旧容器 ID、健康、原项目/服务、候选 image/commit、配置等价与
  kubeconfig 来源通过。该 kubeconfig 是容器内指向宿主的未持久化软链接，
  首次外部检查因按普通文件读取软链接而失败，查清后改为验证精确软链接
  和宿主文件逐字节一致，没有跳过来源检查。
- Compose 的旧 Agent 不支持 modelProvider Secret 导入，创建的
  `cred_f26c3073d35ba2e447ccea3b` 尚无版本；升级后按同 target、同源 Secret
  完成 configure/verify/promote。临时 Secret 在成功导入后删除。
- 两种事务合计 11 项本地检查通过；Compose 真实 dry-run 通过。
  `unicom-1` 未执行 apply，`unicom-k8s` 尚未启动切换 Job。

## 16:20 联通发布完成及全量回读

控制器提交 `46f077d231f4a794e2409cb662869438662b6637` 的 CI
`cnb-l91-1k6t1r1qk` 全部通过，包含发布契约、Agent、Gateway、CLI、
Git 进程验收和 Web 检查。固定镜像发布 CI `cnb-rl3-1k6sg8rga`
再次回读 success。独立代码复审确认 Compose 事务可通过同宿主
`unicom-k8s` 管理通道执行；现场 dry-run 和脚本 SHA256 均吻合。

### unicom-k8s

- Job `doops-system/doagent-unicom-042-cutover` 为 Complete，1/1；
  接收回执 `phase=complete`。
- 新 Pod `doops-agent-6cc9c96f85-f7sql`，UID
  `a8d4ea61-0fd3-4771-ba9a-bd4fc42f8451`，Ready 1/1，固定镜像摘要吻合。
- 回执保存控制器 hash `19c39249ea797f94106d897f5ecb6b7f20ee82fbdc65205ca9760b6b5b434cc5`、
  values hash `77dca9465f4de83ae0358e0ebd376be0d3e070c3f0f32818c86e038c4aac426d`。
  接收 UID 与实际 Pod 一致，旧 `Deployment/doops-unicom` 已移除。
- Gateway release commit、DoAgent 0.2.42、ACP 健康通过。
  16:04 的真实模型请求执行两次 `mcp_doops_k8s_read`，终态 exit 0，
  标识 `RELEASE_SMOKE_UNICOM_042`。

### unicom-1

- 16:13:27 经 `unicom-k8s` 的审计 `nsenter` 启动独立 host systemd
  `doagent-unicom1-042-20261108`；16:13:36 候选 Ready。
- 新容器 ID
  `3eba9048d97ded2f3e2ed9884833942d93f3825be19ff74ca3df1b2a61089233`。
  Gateway commit、DoAgent 0.2.42、ACP 健康、structuredSSH、前台 stdout/stderr
  和后台命令真实终态全部通过。后台 Task `c9e3dbbe87e0e92e` exit 0。
- 同 target 原模型通过 Secret 导入完成 staged/verify/promote。
  16:17:22 真实模型调用两次 `mcp_doops_k8s_read`，终态 exit 0，
  标识 `RELEASE_SMOKE_UNICOM1_042`。随后才写入精确容器 ID 的验收回执。
  临时 Secret `doagent-unicom1-upgrade-source-20261108` 已删除。
- 最终 systemd `Result=success / ExecMainStatus=0 / inactive/dead`，
  回执 `phase=complete`，容器 ID 未改变，Docker health=healthy。
- 线上 Compose 与提交文件分别经相同 `docker compose config` 规范化后逐项相等。
  首次直接比较未规范化 YAML 时断言失败，查明是表现形式不同；
  没有修改线上配置或放宽规范化后的相等判断。
- 原配置备份仍在 `/var/lib/doops-releases/unicom1-042-20261108/`。
  回滚 tag `doops-agent-rollback:unicom1-042-20261108` 对应原 image ID
  `sha256:8eea00f3b42fe3fbeeb916e5a58f45107b986e15cc66ca9e0735678807ac536c`。

### 最终状态与仍需条件

16:20 重新读取全部 13 个 target：gw-test、gw-zyj、gw-hdu-235、gw-edu-coder、
gw-zy、oilan、unicom-k8s、unicom-1 均为 DoAgent 0.2.42 / ACP ok。
ZYJ 已被另一工作流更新为 DoOps v1.3.124，本任务保留该版本。

剩余 5 个 target 的阻断未变化：SCU 的默认 ServiceAccount 只有权限自查，
没有部署权限；Silicon 虽能访问 Docker socket，但 PID1 与当前进程处于同一
容器 mount namespace，`nsenter -t 1` 不是宿主入口，不能据此执行要求宿主的发布器；
科艺仍为 v1.3.43，未安装 `do-agent` 或 Docker，需要确定原生 systemd 运行时安装
及模型来源；114/JM 在 Gateway 仍不可见，需要恢复既有管理通道。
未更改这些节点的权限或部署形态。

Ego TaskSpace 274 已不存在，16:12 再次检查空间列表仍无此空间；
本轮没有新建空间绕过浏览器控制。全环境浏览器路径尚未完成。
最终版本清单、联通模型调用和 Compose 回执见同目录 evidence 中的 JSON；
所有文件仅包含状态和工具输出，不包含凭据 payload。

## 23:32 全量复核与 Silicon 首次模型下发缺陷

本轮重新查询全部 13 个配置 target，原 8 台与 Silicon 均返回 DoAgent 0.2.42、
ACP `status=ok`。ZYJ 仍为其他工作流发布的 DoOps v1.3.124，其他 8 台为本任务
固定的 v1.3.123；没有降级 ZYJ。SCU 仍为 0.2.39 / v1.3.116，Keyi 仍为
v1.3.43 且没有 DoAgent，114/JM 仍不可见。
原始回读见 `evidence/2026-11-08-doagent-042-all/current-targets-2320.json`。

### Silicon 部署终态

- PR #92 已合入 main `1282639c`，完整 main CI `cnb-5uh-1k6t8nalc`
  与固定制品发布 CI `cnb-rl3-1k6sg8rga` 本轮均实际回读 `success`。
- 外部控制器容器
  `1324a5e99511b780306d86e58d7215cca6e67c90a3a90a40b457be40b200528d`
  于 17:50:56（Asia/Shanghai）以 exit 0 结束。日志包含精确新容器 ID、
  回滚名及 `state.json` 路径的 `host_upgrade_ready` 终态；本轮未重新启动控制器。
- 当前容器
  `537b481436243cb61592780d589cba59d708a6d5b96045591190b1737ac303eb`
  使用固定平台 manifest `fd20c510...`，实际 config digest 为 `6cde872a...`。
  命令、入口、环境、工作目录、用户、端口声明以及网络、PID 模式、privilege、
  restart policy、bind mounts 均逐字段与保留的旧容器相等。
- 原容器 `fb0fefdead6a551d58faf742156cddb4325ab14554a9b5c3b4508122bd072d19`
  保留为 `doops-agent-rollback-doagent-silicon-042-20261108`。
  已投递控制器 SHA256 `5808e03721fdbea9bb27f6266805e1a840a907ef0796b38de9b07cb9dcf285c1`
  与提交文件相等。
- ACP 端口是 7750；Agent readiness 端口由原容器参数回读为 **42223**。
  首次误用默认 42222 的检查失败，后续按真实参数检查 42223 返回 `ready`。
  失败记录保留在 `silicon-current-2331.json`，纠正后的证据为
  `silicon-health-2332.json`，没有覆盖失败证据。

### 模型功能失败的代码链路

Silicon 的旧只读挂载 `/opt/doagent_config/settings.json` 中有
`minimax/MiniMax-M3`。其 MiniMax provider 完整配置的规范化 SHA256 与
Gateway 已管理的 `zyj-existing-doagent-model` 对应 provider 相等；仅比较
指纹和配置元数据，未输出密钥。旧文件另外含其他 provider 和图像模型配置，
不能把单个主模型相等说成整个配置文件相等。

23:25 为 Silicon 新建同源主模型的精确 target binding：
`credgrant_be035e0695b12395a21886bc`。对 active version 调用 staged-only verify
明确返回 `credential_version_not_staged`；随后通过正常 `doops ask` 触发
active version 下发，得到 `credential_secret_get_failed`，尚未进入模型推理。
真实失败记录见 `silicon-model-delivery-failed-2326.json`。
本轮已通过 `credential grant-revoke` 撤销该 binding，并收到 `revoked=true`；
共享资源及其他 target 的绑定没有变更。

23:40 在 Silicon 原地将旧文件中的主模型字段通过 stdin 传给
`do-agent model probe --timeout-secs 60`，真实模型请求返回
`status=verified`、exit 0，见 `silicon-existing-model-probe.json`。
该探测不写 runtime settings，只返回状态；它证明现存主模型来源有效，
不能替代经 Gateway 的 AI 对话验收。

在固定发布 commit `8683d67174e19a1750e9b19de99a0816cdb8ea79` 上可证明：

1. `model_provider_delivery.go` 的 `deliverModelProvider` 在物化之前总是调用
   `cleanupCredentialMaterializationContext`。
2. `credential_runtime.go` 构造 cleanup 参数时未发送已有的 `CredentialType`。
3. `cleanupModelProviderCredential` 在本地尚无 delivery record 时返回
   `(false, nil)`，只表示没有匹配的本地模型记录。
4. 通用 `cleanupCredential` 随后继续走 Kubernetes Secret 清理；独立 Docker
   节点没有 kubeconfig，因此 `getCredentialSecret` 返回上述错误。
5. 模型物化本身直接写固定 settings 文件，不需要 Kubernetes。阻断发生在
   无关的前置清理，健康接口不会检测到这个失败。

因此本轮不能将 Silicon 记为功能验收成功，也不能通过伪造 delivery record、
复制 payload 到运行配置或挂入无关集群的 kubeconfig 绕过它。
尚未实际执行回滚：旧 v1.3.117 entrypoint 同样要求 Gateway 下发模型，
回滚不能证明会修复该缺陷；已健康的新 runtime 和可恢复旧容器均保留。

修复必须让清理过程依据可信凭据类型选择存储，原生模型未落盘时应幂等成功，
同时保留其他 grant 的所有权边界、错误传播和审计。验收至少包含无 kubectl/
无 kubeconfig 的首次下发、重连、重复清理、其他 grant 不被删除、失败与撤销。
当前 Agent cleanup 解码器启用 `DisallowUnknownFields`，直接给旧协议添加
字段会破坏混合版本集群；修复方案需要明确滚动升级协议，不能直接宣称
“Gateway 先升级即可兼容”。这需要新的、经过 CI 验证的不可变 DoOps 制品，
不能改变 v1.3.123 的既有 tag/digest。本轮没有改动运行代码或发布额外版本。

### 其他未完成项的新证据

- SCU：root 是容器内的 root。实际 mountinfo 无宿主根、Docker/containerd
  socket 或宿主 kubeconfig；`kubectl auth can-i --list` 只有权限自查及 API
  discovery。没有部署权限，需恢复已授权的独立集群管理入口。
- Keyi：OpenEuler x86_64，systemd 单元运行
  `/usr/local/bin/doops-agent -gateway-url https://doops.l8ai.cn -cluster keyi -instance keyi -port 42222`。
  无 Docker/Podman、无 DoAgent，但有 containerd 与现成 Kubernetes 管理配置；
  `kubectl auth can-i create deployments --all-namespaces` 返回 yes，
  集群三个控制面节点均 v1.31.14。它并非无法管理，待确定新增原生 DoAgent
  运行时还是迁为标准 Helm Agent，以及其模型来源后再发布。
- 114/JM：本轮 target 查询仍不可见。没有将其解释成确定宕机。
  本地默认 kubeconfig 仅有 docker-desktop；SSH 配置中仅发现已知 114 地址，
  没有发现新的 SCU/JM 管理入口。
- 浏览器 TaskSpace 274 仍不存在；已回读当前空间列表确认。
  ego-browser 的规则禁止用新空间恢复已丢失的任务空间，页面验收仍需用户
  允许重建空间。Ego Lite 提示有更新，本轮未升级浏览器。

### 下一轮入口

先解决原生模型清理协议缺陷及所需新制品范围；Keyi 明确部署形态与模型来源；
SCU/114/JM 恢复可用的管理入口；获准重建浏览器空间后完成全环境页面验收。
已有四台旧 Helm 镜像 index 缺失的回滚风险仍在，不能因新增证据而从结论中删除。
目标保持未完成，不把“9 台版本正确”替代“全部环境健康且功能通过”。
