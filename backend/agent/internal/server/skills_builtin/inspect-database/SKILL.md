---
name: 数据库运行与连通性巡检 Skill
description: 通过受管 DoSql MCP 执行只读数据库巡检，凭据不落地
required_mcp: dosql
---

# 数据库运行与连通性巡检 Skill

## 执行步骤（严格只读）
1. 优先调用 `mcp_dosql_read` 列出当前目标可用的受管数据库连接并核查只读连通性。
2. 如存在已绑定数据库，仅执行只读健康查询（版本、当前活跃连接数、锁等待或慢查询概况）。
3. 严禁修改任何表结构或数据，严禁在输出中泄露数据库密码或连接串。

## 输出格式要求
输出简明 Markdown 总结，并附一个 JSON 代码块，包含 `status`、`summary`、`metrics`（数组：`key/label/value/unit/status`）与 `checks`。