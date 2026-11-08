# DoAgent 0.2.42 全环境升级进展

## 范围与制品

用户授权升级全部 13 个已配置 DoOps target。本记录不表示全部完成。
本轮保持原部署方式、模型来源、数据、网络及权限；未发布业务 workload。

- DoAgent `0.2.42`：`08ecb0193767f559844d985cc7365484c61bf9c5`。
- DoOps `v1.3.123`：`8683d67174e19a1750e9b19de99a0816cdb8ea79`。
- CNB release CI `cnb-rl3-1k6sg8rga` 已回读为 success，制品溯源接口确认同一 commit。
- 平台镜像：
  `docker.cnb.cool/l8ai/ai/doops.sh@sha256:fd20c510411d9dde8e2061ed390e3b91877781ade2d4400a39b715280524b02a`。
- 对应 OCI index：
  `sha256:5c6055a7b7a2e4fbab4064cf1c20268b166e7e3915da3e9e92dd42cd011db854`。
  Oilan、ZYJ 的 containerd 报告 index，其他节点报告平台 manifest；
  registry index 内容证明两者关联，不能只按字符串不同认定版本不同。

升级时使用同 commit 的 CLI。后续共享 CLI 被其他工作流更新到 v1.3.124，
未再用它执行本轮 v1.3.123 升级。v1.3.124 与 latest 的移动不改变上述制品身份。

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
| unicom-k8s | 0.2.39；doops-system/doops-unicom revision 5 | 候选拉取超过预检 Job 600 秒；并且原回滚摘要已删除。需同时解决制品拉取与完整可恢复基线。 |
| gw-silicon | 0.2.39 / v1.3.117；独立 Docker，host 网络、privileged、ACP 7750 | 既有宿主 49.51.244.8:2022 外部 SSH banner 超时；目标侧 ubuntu/root 结构化 SSH 无可用认证。恢复已授权管理入口后走 release-host-agent.sh。 |
| unicom-1 | 0.2.35；Docker Compose `/opt/doops-agent/compose.yaml`，容器 doops-agent | release-host-agent.py 明确拒绝 Compose。需要有配置备份、自动恢复、版本/Gateway/功能验收的 Compose 外部控制器；未移除标签或绕过检查。 |
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
