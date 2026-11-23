---
name: 主机软件包与补丁基线巡检 Skill
description: 只读盘点核心安全软件包版本、本地缓存待升级补丁数与重启需求
entrypoint: scripts/inspect.sh
---

# 主机软件包与补丁基线巡检 Skill

## 核心检查项（严格只读）
1. 识别系统包管理器（`dpkg-query` / `rpm` / `apk`）并盘点关键组件版本。
2. 仅基于本地已有缓存统计待升级包数量（绝不执行 `apt update` 或自动安装）。
3. 核查 `/var/run/reboot-required`，提示是否存在等待重启生效的内核/安全补丁。