# DoOps 独立插件包（Plugins）

本目录存放**完全解耦于 DoOps 内核**的标准可安装插件包（不编译进 `doops-gateway` / `doops-agent` 二进制，也不内置进 Agent 基础镜像）。任何自有或第三方运维能力（如 `dodns`、`doemail` 及未来扩展）均按同一规范独立打包，通过控制台「插件中心 → 安装插件包（`.zip`）」或 `POST /v1/skills` 按需安装、启停、升级与卸载，**无需修改一行 DoOps 内核代码**。

## 内含标准插件包

| 插件目录 | 插件 ID | 自声明资产目录（零 DDL） | 自带独立 MCP Server | 默认状态 |
|---|---|---|---|---|
| `plugins/dodns/` | `skill.dodns` | `plugin:dodns:zone`（`DOOPS_PLUGIN_DNS_ZONES`） | `python3 ${SKILL_DIR}/mcp/server.py`（自动备份/校验/回滚） | 按需安装 |
| `plugins/doemail/` | `skill.doemail` | `plugin:doemail:domain`（`DOOPS_PLUGIN_MAIL_DOMAINS`） | `node ${SKILL_DIR}/mcp/server.mjs`（零明文密码/Secret URI） | 按需安装 |

## 打包与安装方式

```bash
# 1. 将任意插件目录打包为标准 .zip（自动排除测试文件）
bash plugins/package-plugin.sh plugins/dodns /tmp/dodns-plugin.zip
bash plugins/package-plugin.sh plugins/doemail /tmp/doemail-plugin.zip

# 2. 在控制台「插件中心」点击「安装插件包 (.zip)」上传，即自动完成：
#    - 严格校验 plugin.yaml（声明式 UI + catalog_schemas + mcp_server）
#    - 激活插件专属动态资产目录（无需数据库迁移）
#    - 自动注册配套 MCP Server（卸载插件时同步清理）
```

如需将插件迁移至独立仓库（例如 `DoDNS` / `DoEmail` 各自独立发版），只需直接保持目录内的 `SKILL.md`、`plugin.yaml`、`scripts/`、`mcp/` 结构即可零改动接入任意 DoOps 集群。
