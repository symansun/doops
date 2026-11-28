---
name: dosecurity
description: DoSecurity 授权低速只读安全评估技能。用于生成低速扫描计划、规范化脱敏资产与凭据引用、分析安全响应头与敏感路径暴露及输出结构化加固建议。
---

# DoSecurity 授权只读安全评估技能

本 Skill 仅服务于**已获明确授权的低速、只读、非破坏性安全评估**。

## 安全红线

1. **禁止高危能力**：严禁爆破、默认口令尝试、SQL 注入利用、Metasploit、DoS 与 fuzzing。
2. **凭据零落盘**：资产记录仅允许保留 `secretRef` 引用名，任何明文 `password`、`token`、`cookie`、`privateKey`、`apiKey` 字段必须直接丢弃，绝不落盘或进入报告。
3. **敏感路径零正文回显**：探测到 `/.env`、`/.git/config`、`/actuator/env` 等敏感路径暴露时，只记录路径、HTTP 状态码与规则名，绝不输出响应正文内容。
4. **覆盖缺口诚实上报**：工具缺失（如未安装 `nuclei`）或请求预算达到上限时必须明确写入 `coverage_gaps`，不得将空结果解释为“无漏洞”。

## 内置脚本

```bash
# 生成低速只读授权扫描计划（自动校验范围边界）
node /root/.agent/skills/dosecurity/scripts/dosecurity_plan.mjs plan \
  --root-url https://app.example.com \
  --root-domain example.com \
  --authorized-by "ops-lead"
```
