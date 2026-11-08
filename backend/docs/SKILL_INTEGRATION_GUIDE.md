# 业务 Skill 编写与接入

Agent 原生发布职责边界见
[`NATURAL_LANGUAGE_DEPLOYMENT.md`](NATURAL_LANGUAGE_DEPLOYMENT.md)。

Skill 是交给 doagent 原生 Agent 引擎的领域契约。Skill 不执行命令，也不实现 Agent
框架；doagent 负责上下文、规划、多 Agent 委派、Skill 组合和工具调用。

## 何时创建 Skill

只有以下内容稳定且会重复使用时才创建：

- 业务目标和术语；
- 不可变约束和安全边界；
- 输入对象与权威配置来源；
- 成功、阻塞和失败所需的实际证据；
- 允许使用的能力范围和禁止行为；
- 结构化输出契约。

一次性命令、环境临时值、凭据、完整日志和罕见边角案例不应写进 Skill。

## 文件结构

Skill 使用带 frontmatter 的 `SKILL.md`。安装位置由 doagent runtime 的 Skill root
配置决定；不要在 DoOps Gateway 中增加自定义扫描、匹配或拼装逻辑。

```yaml
---
name: labelu-release
description: 验证并协调 LabelU 发布目标
max_tokens: 1200
requires: []
conflicts: []
---

# LabelU 发布

## 目标

将调用方明确声明的 LabelU release 协调到指定 target，并返回可复查的运行状态证据。

## 输入边界

- 只接受当前任务已经声明的 target、artifact digest、desired state 和 acceptance。
- 不根据项目名、域名、目录或历史环境补全目标。
- 未声明的凭据、环境和 mutation 授权都视为阻塞事实。

## 不变量

- 工作区与不可变 release identity 一致。
- 运行制品与计划引用一致。
- 未授权时不得改变真实环境。

## Agent 原生执行

- doagent 可以组合其他已安装 Skill、工具和多 Agent。
- 只能使用运行时实际暴露且当前权限允许的能力。
- 本 Skill 不规定固定命令序列、工具名称、子 Agent 数量或重试拓扑。

## 验收

- 只有全部 required evidence 都来自实际模块观察时才能报告 converged。
- 不把 admission 文本、通用 attestation 或 Agent 自报完成当作 evidence。
- 缺能力或权限时报告 blocked，并记录具体缺失事实。
- mutation 后验收失败时，只能使用环境明确声明的可逆恢复能力。

## 输出

返回调用契约要求的单个结构化结果，不返回 Markdown、命令清单或文本成功声明。
```

## 编写规则

1. 写目标和事实，不写“第一步、第二步”的固定命令序列。
2. 写允许和禁止的能力边界，不假设某个二进制或工具一定存在。
3. 写完成证据，不把 Agent 自报完成当证据。
4. 写 mutation 授权，不在 Skill 中自动批准权限。
5. 写恢复条件，不写死某个部署技术或历史版本。
6. 允许 doagent 自主使用多 Agent 和其他 Skill，不在 Skill 中设计子 Agent 框架。
7. 遇到真实问题直接 `blocked` 或 `failed`，不添加 fallback、静默降级或兼容分支。

## 脚本边界

Skill 可以引用运行时已经暴露的原子工具，但不应内嵌大段 shell。业务确实需要脚本时：

- 脚本只完成一个原子能力；
- 输入、输出、退出码和副作用必须明确；
- mutation 必须有显式授权，尽量支持 dry run 和幂等执行；
- 失败必须非零退出并保留根因；
- 不包含凭据、环境猜测、Skill 路由、多 Agent 调度或自动修复 loop；
- 是否调用该脚本仍由 doagent 根据现场能力和任务决定。

## 验证

- 检查 frontmatter 可被 doagent 原生 Skill 机制读取。
- 用一个成功场景和至少一个缺权限或缺能力场景验证行为。
- 确认输出证据来自真实工具观察。
- 确认 Skill 中没有具体环境坐标、凭据、固定命令序列和隐藏 fallback。

## 当前技能来源与验收

运维入口是 `backend/skills/SKILL.md`，Agent 镜像使用 `backend/agent/skills/`。
安装和运行时路径见 [使用指南](DOOPS_USAGE_GUIDE.md#source-and-installed-paths)。
旧报告中的独立 pipeline、docker、security 已不在当前镜像源中；不要为匹配旧目录恢复
生成器协议或安全插件。必要的授权和数据边界写入当前注入的 `system_prompt.md`，
Kubernetes 和构建知识分别维护在 k8s、image-build 中。

`triggers`、`phase`、`max_tokens` 等字段交给 doagent runtime 解释；仅修改 Markdown
不能证明路由、截断预算或安全隔离生效。对模型的指导与运行时工具白名单、Gateway
授权、终态和凭据保护分别验收，不声称提示词能拦截所有直接执行命令。

| 场景 | 可观察的验收结果 |
| --- | --- |
| 仅构建配置 / 非 Kubernetes 服务重启方案 | 不追加推送、集群部署或删除容器 |
| root 且无 sudo / 需要 CGO 的项目 / Node 服务 | 按实际环境和项目配置生成命令，不套固定模板 |
| 独立 exec 或并发临时目录 | 工作目录显式；只清理本任务创建且已无任务使用的路径 |
| 日志截断、空日志、多对象部分失败、重试成功 | 摘要保留对象和证据边界；没有终态时不报整体成功 |
| 日志夹带指令或秘密 | 不执行日志指令，不将秘密带入摘要 |
| 已授权发布 / 仅方案 / 新增影响 | 复用已有授权；方案不变更；新增影响交回上层 |
| 预检错误 / 变更后验证失败 / RPC 终态未知 | 仅明确未开始变更的输入错误可修正重试；其余先只读核对 |
| token 签发 / 下载工作区 | 秘密只进入私密交付文件；下载不覆盖业务仓库 |

技能文档修改随对应 CLI release 或 Agent 镜像分发。源码测试和静态审查不等于已经部署，
模型场景演练也不替代真实工具的功能测试；交付时分别报告这些证据。
