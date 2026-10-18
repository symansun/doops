# 物理机入口记录（2026-10-05）

归档自 `backend/docs/PHYSICAL_HOST_ACCESS.md`，是当次会话事实，不是当前授权。

- `49.51.244.8`：SSH 用户 `ubuntu`、端口 `2022`，已登录；已有
  `known_hosts` 的 `[49.51.244.8]:2022` 记录需核对。其 `doops-agent` 是独立 Docker
  容器，`network=host`、`privileged=true`，无 Compose labels，也无 K8S；发布须保持这些配置。
- `43.173.87.215`：另一个主机入口，独立 Docker、无 K8S；目前仅授权只读检查，
  不是前一地址的别名，不得对其停止容器、换镜像或发布。端口和账号须按其独立证据确认。

上一轮检查 22 且未核对已有 2022 主机密钥记录，不能据此宣称 SSH 未登录或主机不可达。
后续必须先核对地址、用户、显式端口和已有认证状态；密码不得写入任何文件、示例或日志。
用户提供的 43/49 密码仅经外部 bootstrap stdin 建立管理 SSH 认证，不传给 Agent。
外部管理 SSH 成功与 Agent structuredSSH 验收分别记录；后者验证协议、argv 派发与
预期失败终态，不宣称目的地主机成功认证。真实访问仍须 Agent 内已有授权 SSH 认证材料。
