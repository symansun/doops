# DoOps Roadmap

## v0.2.0 —— 加固与发布（当前迭代）

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

---

## v0.2.2 迭代计划（2026-11-08 制定）

主题：可观测性 + 上手体验 + 安全加固 + Skills 生态（四线并行，P0 先行）

### P0 开发
1. **gateway /metrics（Prometheus）**：在线 agent 数、工具调用计数（按 action/结果）、护栏拦截计数、WS 连接数；端点默认仅监听回环或需 token
2. **install.sh 直连 Release**：默认从 GitHub Release 下载对应平台二进制 + sha256 校验，`--from-source` 保留源码构建路径

### P1 开发
3. **token TTL 与轮换**：用户 token 支持过期时间，CLI 侧过期自动提示重新登录（errhint 已分层，接入即可）
4. **Skills 示例库**：`examples/skills/` 新增 3 个开箱即用示例（nginx 巡检、证书到期巡检、日志收集打包），各含 README + 测试说明

### P0 测试
5. /metrics 端点单测 + E2E 冒烟中加入 metrics 断言
6. install.sh 在干净容器（无 Go）中跑通下载安装
7. token 过期/轮换的单测与集成测试

### 发布检查单
- [ ] 三模块 build + test 全绿
- [ ] pytest 契约测试通过
- [ ] E2E 冒烟（process 模式）通过
- [ ] CHANGELOG v0.2.2 定稿
- [ ] tag v0.2.2 → Release 产物冒烟
