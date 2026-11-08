# 发布规范（release-flow）

把团队 DevOps 发布规范固化成一条可执行命令，规范要点：

1. **只发 tag**：`--version` 必须是 `vX.Y.Z` 形式，游离分支直接拒绝；
2. **预发直接发**：staging target 发布 + 健康检查，验证通过才进入生产环节；
3. **生产走审批**：prod target 命中 gateway 审批管控（`-approval-targets`）时，脚本自动创建审批单、等待审批人 `doops admin approve`、获批后携带 `--approval` 重放**同一条**命令；
4. **全程同 session**：审计日志中预发/生产两段可对照回放，审批单状态流转（pending → approved → consumed）落盘可查。

目标节点假设：应用位于 git 仓库目录，用 systemd 服务承载，发布后通过 HTTP 健康检查确认。

## 前置条件

- 本机已安装 doops CLI，并 `doops login` 到 staging 与 prod 两个 target
- gateway 以 `-approval-targets "prod/*"`（或覆盖 prod target 的等价 glob）启动
- 审批人账号持有 admin 授权（gateway 播种时 `grant -actions admin`）
- 发起人账号如需脚本自动轮询审批状态，也需要 admin 授权；否则可注释掉轮询段、改为人工确认后重放

## 用法

```bash
bash examples/skills/release-flow/run.sh \
  --version v1.4.2 \
  --staging-target staging --prod-target prod \
  --app-dir /srv/app --service myapp \
  --health-url http://127.0.0.1:8080/health
```

典型输出：

```
==> [1/4] 预发发布 v1.4.2 -> staging
✅ 预发发布完成
==> [2/4] 生产发布 v1.4.2 -> prod（触发审批闸）
审批单已创建: apr_1a2b3c4d5e6f
请审批人执行: doops admin approve --id apr_1a2b3c4d5e6f
==> [3/4] 等待审批（最长 10 分钟，轮询审批单状态）
审批单 apr_1a2b3c4d5e6f 已通过
==> [4/4] 携带审批单重放生产发布
✅ 生产发布完成（v1.4.2 -> prod，审批单 apr_1a2b3c4d5e6f）
```

审批被拒绝或 10 分钟内未审批，脚本以非零退出，生产侧不会有任何变更。

## 测试说明

1. 先跑 `bash deploy/e2e/smoke.sh --mode process`，其中的审批流断言会验证「拦截 → approve → 重放 → 一次性消费」全链路，与本脚本依赖的机制相同。
2. 手工演练：在测试环境起 gateway（`-approval-targets "e2e-prod/*"`），准备两个 target：
   - `e2e`（充当预发，agent 在线）
   - `e2e-prod`（充当生产，可指向同一 agent 以观察真实执行，或不挂 agent 观察闸机行为）
3. 在模拟应用目录（git 仓库 + 一个 tag）上执行：

   ```bash
   bash examples/skills/release-flow/run.sh \
     --version v0.0.1 \
     --staging-target e2e --prod-target e2e-prod \
     --app-dir /tmp/fake-app --service fake.service
   ```

   预期：预发段直接成功；生产段先被拦截并打印 `apr_...`，另开终端执行
   `doops admin approve --target e2e-prod --id <apr_...>` 后，脚本 10 秒内检测到
   approved 并重放发布。
4. 反向用例：审批人执行 `doops admin reject --id <apr_...>`，脚本应以「审批单已被拒绝」非零退出；不带 `--approval` 手工重放同一条命令应仍被拦截。
