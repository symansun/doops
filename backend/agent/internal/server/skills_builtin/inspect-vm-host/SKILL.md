---
name: 虚机与物理机系统巡检 Skill
description: 只读采集主机与虚机 CPU、内存、去重磁盘容量、运行时长、进程与内核安全基线
entrypoint: scripts/inspect.sh
---

# 虚机与物理机系统巡检 Skill

## 执行方式
- **脚本直跑（默认）**：执行 `scripts/inspect.sh`，只读采集系统资源与安全基线，百毫秒输出标准 Markdown + JSON（含 `metrics[]`、`checks[]`、`charts[]`）。
- **AI 深度诊断**：附加自然语言提示词或选择「AI 驱动」时，`do-agent` 先运行 `scripts/inspect.sh` 获取真实数据，再结合上下文做根因分析。

## 检查项与口径
1. **运行时长与环境边界 (M1)**：读取系统 uptime，并在容器无 lxcfs 时明确标注宿主机共享内核口径。
2. **CPU 与内存水位 (M2)**：Linux 采样 250ms `/proc/stat` 差值与 `/proc/meminfo`（`MemTotal - MemAvailable`）。
3. **去重磁盘存储容量**：排除伪文件系统（tmpfs/devtmpfs/shm/proc/sysfs），对同盘多挂载点（如 overlay 与底层 `/dev/vda1`）按设备与容量特征去重，防止总容量重复累加。
4. **进程健康 (M3)**：统计总进程数与僵尸进程数。
5. **内核参数基线 (M4)**：只读核验 `kernel.kptr_restrict`、`kernel.dmesg_restrict`、`kernel.randomize_va_space`、`fs.protected_hardlinks`、`fs.protected_symlinks`、`net.ipv4.tcp_syncookies`。
6. **账户基线指纹 (M5)**：只读计算 `/etc/passwd` 与 `/etc/group` 的 SHA-256 指纹（不输出敏感内容）。

## 约束
严格只读，不修改宿主机配置，不读取任何凭据载荷。