---
name: 容器运行时巡检 Skill
description: 自动探测 Docker / nerdctl / Podman 容器状态、CPU 与内存上限占比
entrypoint: scripts/inspect.sh
---

# 容器运行时巡检 Skill

## 核心检查项
1. 自动探测本机可用的容器运行时（`docker` / `nerdctl` / `podman`）。
2. 采集运行中容器列表与实时 CPU、内存、内存上限（`memory_limit`）占比。
3. 识别超过 85% 内存上限的高风险容器与非运行/不健康容器。