---
name: 机器部署分析与自动打标 Skill
description: 零 Token 只读挖掘机器上实际部署的数据库与版本、语言运行时（Java/Python/PHP/Node/Go 等）、Web/中间件与业务系统及版本，自动生成符合机器台账规范的 key=value 标签建议
entrypoint: scripts/inspect.sh
---

# 机器部署分析与自动打标 Skill

## 执行形态与能力定位
- **双轨执行**：
  - **脚本直跑（默认，零 Token）**：`scripts/inspect.sh` 基于纯 Python 3 标准库，在 1–3 秒内只读交叉核验操作系统、运行进程、监听端口、容器镜像、Nginx 站点配置及安全构建元数据清单，输出结构化部署清单（`workloads[]`）与符合机器台账校验规范的标签建议（`suggested_tags[]`）。
  - **AI 深度业务语义识别（按需触发）**：针对非标准自研业务系统、内部服务代号、多进程耦合架构，可结合脚本结果做只读深入语义研判，补充业务系统名、版本与归属角色标签。

## 挖掘范围与版本识别口径
1. **操作系统**：读取 `/etc/os-release` 生成 `os=<distro>-<version>`。
2. **数据库与数据存储（含版本）**：MySQL、MariaDB、PostgreSQL、Redis/Valkey、MongoDB、Elasticsearch/OpenSearch、ClickHouse、达梦（DM）、人大金仓（Kingbase）、TiDB、OceanBase、Oracle、SQL Server、InfluxDB、Milvus、MinIO 等。版本号优先取容器镜像 Tag、本地二进制 `--version`/`-V` 或包管理器版本；无法确认时如实标为 `unknown`，绝不编造。
3. **语言运行时（含版本）**：Java（JDK 8/11/17/21 与 JAR/Tomcat/Spring Boot）、Python（Gunicorn/Uvicorn/Celery/Django/FastAPI/Odoo/vLLM）、PHP（php-fpm/Apache/WordPress/Laravel/ThinkPHP）、Node.js（Next.js/Nuxt/NestJS/Express/PM2）、Go、.NET、Ruby、Rust。默认优先识别**正在运行或实际托管业务**的运行时，开启 `INCLUDE_INSTALLED_RUNTIMES=true` 后额外盘点已安装但未常驻的运行时。
4. **Web 服务器与中间件（含版本）**：Nginx / OpenResty / Tengine、Apache httpd、Tomcat、Caddy、HAProxy、Traefik、Kafka、RabbitMQ、RocketMQ、NATS、EMQX、Nacos、Etcd、Zookeeper、Prometheus、Grafana、Zabbix 等。
5. **业务系统与应用（含版本）**：
   - 运行中 Java `-jar` 包名及 `META-INF/MANIFEST.MF`（`Implementation-Title` / `Implementation-Version` / `Spring-Boot-Version`）；
   - Node.js `package.json`（仅取 `name`/`version` 及顶层框架依赖名）；
   - PHP `composer.json` 与 `wp-includes/version.php`（识别 WordPress/Laravel/ThinkPHP/Drupal 等）；
   - Python `pyproject.toml`（仅取 `name`/`version` 与主流框架名）；
   - Nginx 虚拟主机 `server_name` / `root` / `proxy_pass` 映射、自定义 `systemd` 单元及运行中容器业务镜像（GitLab/Harbor/Jenkins/Gitea/Dify/JumpServer/Ollama/Halo 等）。

## 安全与打标红线（严格遵守）
1. **绝对只读、零凭据泄露**：严禁读取 `.env`、`application.yml`、`wp-config.php`、`settings.py`、`config.php`、私钥或凭据文件；进程命令行与镜像地址中的敏感参数/口令必须先脱敏再入证据字段。
2. **严守机器台账标签规范**：生成的 `suggested_tags[]` 必须 100% 符合 Gateway `inventory_machine_tags` 校验规则（小写、单条 ≤ 32 字符、最多 20 条、仅允许文字/数字/`.`/`-`/`_` 与单次 `key=value`，绝不含凭据类敏感词）：
   - `role=<database|web|app|cache|mq|ai-gpu|k8s-node|monitor>`
   - `os=<distro>-<ver>`
   - `db=<engine>-<major.minor>`
   - `runtime=<lang>-<major.minor>`
   - `web=<server>-<major.minor>` / `mq=<broker>-<major.minor>`
   - `app=<name>-<major.minor>`
3. **确认后再写台账**：本 Skill 只产出只读挖掘证据与标签建议，**绝不静默修改** CMDB 机器台账；管理员在控制台预览差异并确认后，经既有受审计的 `POST /v1/assets/machines`（乐观锁 `expected_revision`）合并写入。

## 输出格式
输出简明 Markdown 总结并附一个完整 JSON 代码块，包含 `target`、`status`、`summary`、`metrics`、`checks`、`charts`、`workloads`、`suggested_tags`、`sites`、`coverage_gaps`。
