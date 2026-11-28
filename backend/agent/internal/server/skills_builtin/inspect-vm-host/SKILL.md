---
name: 虚机与物理机系统巡检 Skill
description: 只读采集主机与虚机 CPU、内存、去重磁盘容量、物理块设备 I/O、网卡吞吐、NVIDIA GPU、进程与内核安全基线
entrypoint: scripts/inspect.sh
exec_mode: auto
---

# 虚机与物理机系统巡检 Skill

## 执行方式
- **脚本直跑（默认）**：执行 `scripts/inspect.sh`，只读采集系统资源与安全基线，百毫秒输出标准 Markdown + JSON（含 `metrics[]`、`checks[]`、`charts[]`），零大模型 Token 消耗。
- **AI 深度诊断**：附加自然语言提示词或选择「AI 驱动」时，`do-agent` 先运行 `scripts/inspect.sh` 获取真实数据，再结合上下文做根因分析。

## 检查项与口径
1. **运行时长与视角边界 (M1)**：读取系统 uptime，通过 `/.dockerenv`、`/proc/1/cgroup` 与 `/proc/1/cmdline` 区分「宿主机原生视角」「容器（`hostPID=true`）」与「纯容器视角（未开 `hostPID`）」。
2. **CPU 与内存水位 (M2)**：Linux 采样 250ms `/proc/stat` 差值、1 分钟负载与 `/proc/meminfo`（`MemTotal - MemAvailable`），输出 `cpu_total_cores`、`cpu_percent`、`memory_percent` 与 Top 5 CPU/内存进程名（仅 `comm`，不含命令行参数）。
3. **去重磁盘存储容量**：排除伪文件系统（`tmpfs`/`devtmpfs`/`shm`/`proc`/`sysfs`），对同盘多挂载点（如 `overlay` 与底层 `/dev/vda1`）按设备与容量特征去重，防止总容量重复累加。
4. **物理块设备磁盘 I/O 与延迟 (M6)**：读取 `/proc/diskstats`，排除虚拟设备（`loop`/`dm-`/`md`/`zram` 等）与分区计数器，统计读写次数、字节数与最高平均延迟（告警 50ms / 严重 200ms）。
5. **物理网卡吞吐 (M7)**：与 CPU 同窗 250ms 采样 `/proc/net/dev`，排除 `lo` 与虚拟桥接/容器网卡（无物理网卡时回落非 `lo` 接口），统计收发速率（`net_bytes_per_sec`）与错包/丢包增量。
6. **NVIDIA GPU 算力与显存 (M8)**：当主机存在 `nvidia-smi` 且检出 GPU 时，采集每卡利用率、显存占比、温度与功耗（显存 ≥92%/98% 或温度 ≥85°C/92°C 告警/严重），并自动追加 GPU 水位柱状图；无 GPU 节点不产生误报或伪造 0 值。
7. **进程健康 (M3)**：统计总进程数与僵尸进程数（>5 告警）。
8. **内核参数基线 (M4)**：只读核验 `kernel.kptr_restrict`、`kernel.dmesg_restrict`、`kernel.randomize_va_space`、`fs.protected_hardlinks`、`fs.protected_symlinks`、`net.ipv4.tcp_syncookies`。
9. **账户基线指纹 (M5)**：只读计算 `/etc/passwd` 与 `/etc/group` 的 SHA-256 指纹（不输出敏感内容）。

## 约束
严格只读，不修改宿主机配置，不读取任何凭据载荷，不输出进程完整参数。
