# DoOps Roadmap

## v0.3.0 迭代计划（2026-11-08 制定，来源：11-08 会议纪要）

主题：控制台打通 + DevOps 规范固化 + 审批与审计强化 + 飞书 AI 集成

会议纪要要求："DoOps 演进要有排期计划"，下一步两个主方向：①打通控制台，让网站 Agent 直接完成部署任务；②把 DevOps 规范/工程化经验固化进工具。

### P0 开发
1. **控制台最小闭环（设计先行）**：Web 控制台 + AI 助手，覆盖资源查询（服务器/K8s）、部署变更两个场景；先交控制台架构设计文档与 API 草案，再做最小可点通闭环
2. ✅ **DevOps 发布规范 skill 化**：`examples/skills/release-flow`（2026-11-08 完成）——只发 tag、预发直接发、生产走审批闸自动建单/轮询/重放，全程同 session 可审计回放
3. ✅ **生产发布审批流**（2026-11-08 完成）：gateway `-approval-targets` 按 cluster/instance glob 管控变更类调用，审批单（apr_*，24h TTL，参数指纹校验）approve 后 `--approval` 重放放行，一次性消费；CLI `admin approvals list/approve/reject`；审批操作落审计

### P1 开发
4. **审计强化**：审计导出（JSONL/CSV）、留存策略、高危操作告警通道；客户真实上线前必须完成
5. ✅ **平滑发布/灰度**（2026-11-08 完成）：`examples/skills/blue-green-release`——双容器 blue-green + nginx upstream 切换，可选 canary 权重灰度，`--rollback` 秒级回滚
6. **飞书 AI 集成**：飞书机器人 @ 机器人下达运维任务（webhook → doops gateway 桥接），与数字员工工程对接

### 运营事项（线下执行，非代码）
- 给小浩开 DoOps 账号 + GitHub 代码权限

### 发布检查单
- [ ] 控制台设计文档评审通过
- [ ] 发布规范 skill 在 staging 环境走通一次完整发布
- [x] 审批流单测 + E2E（approval_test.go 全绿；smoke.sh 审批流 6 步断言通过）
- [ ] CHANGELOG v0.3.0 定稿
- [ ] tag v0.3.0 → Release 产物冒烟

## v0.2.2（✅ 已发布，2026-11-08）

主题：可观测性 + 上手体验 + 安全加固 + Skills 生态

### P0 开发（✅ 已完成）
1. **gateway /metrics（Prometheus）**：在线 agent 数、WS 连接数、活跃操作数、工具调用计数（按 action/status）、护栏拦截计数；回环放行，非回环需 `DOOPS_METRICS_TOKEN`
2. **install.sh 直连 Release**：默认从 GitHub Release 下载 CLI + `doops-skill.tar.gz` 并做 sha256 校验；`--version` 锁版本，`--from-source` 保留仓库内预构建路径

### P1 开发（✅ 已完成）
3. **token TTL 与轮换**：`token list` / `token revoke -id` 轮换管理，吊销即失效（TTL 过期校验与 CLI 401 重新登录提示此前已就绪）
4. **Skills 示例库**：`examples/skills/` 三个开箱即用示例（nginx 巡检、证书到期巡检、日志收集打包），各含 README + 测试说明

### P0 测试（✅ 已完成）
5. /metrics 端点单测 + E2E 冒烟 metrics 断言
6. install.sh 干净环境（临时 HOME、无仓库检出）Release 下载安装冒烟通过
7. token 过期/轮换单测（store 层）通过

## v0.2.0 —— 加固与发布（✅ 已完成）

主题：把 v0.1 补齐的护栏与错误体系做成可发布、可验证、可演示的正式版本。

### P0 开发（✅ 已完成）

| # | 事项 | 说明 | 验收标准 |
|---|------|------|----------|
| 1 | 护栏 scope 豁免通道 | gateway 转发 exec 时携带调用方 scope 摘要（协议帧字段），agent 侧 `admin` / `exec:privileged` scope 可豁免护栏 | 管理员 token 命中规则可放行且审计记录豁免原因；普通 token 仍被拦 |
| 2 | GitHub Release 流程 | tag 触发 CI：构建 4 平台 CLI 二进制 + agent 镜像，自动发 Release 附件 | `git tag v0.2.0 && git push --tags` 后 Release 页自动产出可下载产物 |
| 3 | CHANGELOG.md 维护 | 按 Keep a Changelog 格式，从 v0.2.0 开始 | 每个版本节点含 Added/Fixed/Security 分节 |
| 4 | 英/俄 README 配图 | slogan.svg、architecture.svg 出英文版（俄版复用英文图），按语言切换 | README.en.md/ru.md 中图片为英文文案 |

### P0 测试（✅ 已完成）

| # | 事项 | 说明 | 验收标准 |
|---|------|------|----------|
| 5 | E2E 冒烟（CI 可选 job） | docker compose 起 gateway + agent，CLI 跑 login → exec → 断言审计落盘 | CI 绿；失败时输出三方日志 |
| 6 | 护栏模糊测试 | `go fuzz` 跑 dispatcher 的规范化+匹配路径 | 30s fuzz 无 panic；语料含大小写/空白/引号混淆样本 |
| 7 | 版本兼容矩阵 | 新旧 CLI × 新旧 agent 两两组合冒烟 | 组合矩阵全过或文档标注不兼容项 |

### P1（视进度纳入）

| # | 事项 | 说明 |
|---|------|------|
| 8 | 演示站健康检查 | 定时探测演示站 `/health`，异常时通知 |
| 9 | 审计查询增强 | CLI 支持 `blocked=1` 过滤与 JSONL 导出 |

### 发布检查单

- [ ] 三个 Go 模块 `go build ./... && go test ./...` 全绿
- [ ] 镜像契约测试通过
- [ ] CHANGELOG v0.2.0 章节完整
- [ ] 三语 README 命令与二进制行为一致（抽查 exec/login/targets）
- [ ] Release 页附件可下载、可运行（`--help` 冒烟）

## 已完成（v0.1.x）

- agent 命令护栏：10 类规则、审计落盘（`blocked=1 rule=<id>`）、`DOOPS_BLOCK_*` 配置
- CLI 错误分层可操作化（fatalErr + Hint），修复 gateway-only 校验错位回归
- 三语 README（中/英/俄）、界面预览、架构图、微信公众号指南

