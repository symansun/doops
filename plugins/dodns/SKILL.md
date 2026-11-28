---
name: dodns
description: DoDNS 独立可安装插件包：零 dig 依赖核查 BIND/Unbound 状态、Zone 语法、开放递归、AXFR 与解析延迟，并自带独立 stdio MCP Server 执行带备份与失败自动回滚的受控 DNS 变更。
entrypoint: scripts/inspect.sh
exec_mode: auto
---

# DoDNS 基础设施治理插件

本插件包为完全自包含的 DoOps 外部可安装插件（不耦合 DoOps 内核二进制或基础镜像），集成观测巡检脚本、资产目录 Schema（`plugin:dodns:zone`）与独立 stdio MCP Server（`mcp/server.py`）。

## 核心安全红线

1. **权威与递归分离**：BIND 权威节点须配置 `recursion no`；Unbound 递归器仅监听私有回环地址，禁止公网开放递归与未授权 AXFR。
2. **变更四步闭环与自动回滚**：所有 Zone 写变更通过独立 MCP（`mcp_dodns_record_add` / `mcp_dodns_record_replace` / `mcp_dodns_record_delete`）自动完成「时间戳备份 → SOA 序列号递增 → `named-checkzone` 校验 → 可选 `rndc reload`」，校验或重载失败自动从备份逐字节还原。
3. **零明文凭据**：严禁在参数、提示词、资产目录或输出中携带 TSIG 密钥、`rndc.key` 或 AliDNS AccessKey Secret；云 DNS 仅通过 DoOps 托管 `opaqueSecret` 运行时 `0600` 文件注入。

## 自带 MCP 工具（`mcp/server.py`）

- 只读工具：`query`、`check_recursion`、`zone_render`、`zone_check`
- 受控变更工具：`record_add`、`record_replace`、`record_delete`、`alidns_ensure`
