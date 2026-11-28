---
name: Kubernetes 容器集群巡检 Skill
description: 只读检查 K8s Node、异常/高重启 Pod、控制器副本、PVC 绑定与 Warning 事件
entrypoint: scripts/inspect.sh
required_mcp: doops
---

# Kubernetes 容器集群巡检 Skill

## 核心检查项
1. **节点健康**：校验全部 Node Ready 状态与 Memory/Disk/PID 压力。
2. **Pod 异常与高重启**：识别 CrashLoopBackOff、OOMKilled、Pending、Failed 及重启次数 ≥ 5 的容器。
3. **控制器副本**：校验 Deployment 与 StatefulSet 的 readyReplicas == spec.replicas。
4. **PVC 与事件**：检出非 Bound 状态 PVC 与近期 Warning 事件。
5. **实时资源用量**：通过 `kubectl top nodes` 与 `kubectl top pods -A` 采集节点（`node_usage`）及 Top 40 Pod（`pod_usage`）的 CPU/内存用量并自动桥接至 Agent 监控曲线；metrics-server 不可用时仅记告警检查项，不掩盖集群资源状态。

## 协作方式
- 默认执行 `scripts/inspect.sh` 快速采集结构化结果。
- 附加提示词使用 AI 驱动时，可结合 `mcp_doops_k8s_read` 深入排查具体异常 Pod 日志与事件。