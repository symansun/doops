---
name: physical-host-access
description: 通过已授权入口查看物理机硬件，明确容器、VM、SSH 与新安装的边界。
scope: doops-operations
---

# 物理机 / 宿主机访问与硬件检查

查看硬件不要求安装新 Agent。先根据已有拓扑、资产记录和明确授权，选择已有
物理机 target 或经 VM target 发起的 SSH；用户说“已进入宿主机”不是安装授权。

## 1. 确认目标与可见范围

- 核对 Agent 容器、所在节点（VM 或物理机）、hypervisor 与目标物理机的对应关系，
  以及已授权的地址、账号、入口和只读检查范围。target 在线不代表它就是物理机。
- 容器看到的是其权限与设备暴露范围内的信息；VM 只能看到虚拟设备或已分配的
  passthrough / vGPU，不能据此推断宿主机完整硬件清单。
- `nsenter` 只能进入当前内核中有权限访问的命名空间。VM 内即使能进入 VM 节点
  命名空间，也不能跨越 hypervisor 进入物理机；容器有特权也不等于已进入宿主。
- 不扫描未知网段、不猜地址或密码、不尝试越权入口。缺少映射、授权或可用入口时，
  说明缺失事实并请资产负责人补充，不以自动安装或绕过认证代替。

## 2. 使用已授权入口

以下 `<...>` 是需替换的占位符；`--target` 使用本地已配置的 target 名称，
不是任意拼接的 `cluster/instance`。所有远程示例显式携带 session。

### 已有物理机 target

确认该 target 对应目标物理机，再执行只读命令：

```bash
doops -session <s> targets --target <physical-target>
doops -session <s> exec --target <physical-target> -- hostname
doops -session <s> exec --target <physical-target> -- uname -a
```

`targets` 按所选配置的精确 `cluster/instance` 过滤，不是列出整个 Gateway 的资产。
`exec` 在 Agent 执行环境中运行；若仍是容器视角，不要将其 hostname 或设备列表
当作完整宿主证据，也不要为了检查而自动增加特权、挂载或重装。

### 经已有 VM target SSH 到物理机

这是已授权入口，不是要求先尝试安装的降级路径。前提是 VM Agent 到已知宿主 SSH
地址可达、宿主允许该账号登录，且 Agent 执行环境已有合法认证材料和已核验主机密钥。

```bash
doops -session <s> ssh --target <vm-target> --host user@<physical-host> \
  --option=BatchMode=yes --option=StrictHostKeyChecking=yes -- hostname
```

`--target` 选择发起 SSH 的 Agent，`--host` 才是目标物理机；`--` 后按 argv 传递
远端命令。需要管道或复合命令时，须确认目标有对应 shell，再显式传
`bash -lc '<script>'`，不能用 `exec -- ssh ...` 绕过结构化 SSH。

当前 CLI 的 `ssh` 没有 `--credential`，凭据系统也没有 generic SSH credential
injection：不能宣称创建、写入或授权托管凭据后就能自动 SSH。
`--option=IdentityFile=<agent-side-path>` 仅引用 Agent 执行环境中已有、已授权的
文件，不会上传本机私钥或注入托管凭据。认证或主机密钥缺失时交由授权管理员配置，
不要把密码、私钥或 token 写进参数、prompt、日志或文档，也不要关闭主机密钥校验。

已有并获授权的 Proxmox 管理面、IPMI / iLO / Redfish 或控制台可用于核对拓扑与硬件；
按该入口实际权限和平台工具操作，不假设 DoOps 内置这些 API，也不自动扩大到写操作。

## 3. 判读检查结果

先将主机身份、OS、地址及可读的资产标识与拓扑记录交叉核对；单一 IP、hostname
或 GPU 输出都不能独立证明已到物理机。以下是经 SSH 在 Linux 宿主检查工具的示例：

```bash
doops -session <s> ssh --target <vm-target> --host user@<physical-host> \
  --option=BatchMode=yes --option=StrictHostKeyChecking=yes -- sh -c 'command -v lspci'
doops -session <s> ssh --target <vm-target> --host user@<physical-host> \
  --option=BatchMode=yes --option=StrictHostKeyChecking=yes -- lspci -nn
doops -session <s> ssh --target <vm-target> --host user@<physical-host> \
  --option=BatchMode=yes --option=StrictHostKeyChecking=yes -- nvidia-smi -L
```

按需逐条执行，不把示例当作必跑脚本：先确认工具可用，再检查相应设备；
`nvidia-smi` 仅用于有相关工具和驱动的 NVIDIA 场景。其他 OS 使用已确认支持的只读工具。

- `command not found` 或 `command -v` 非零：工具不可用，不代表 GPU 不存在；
  安装工具需要单独授权。
- `lspci` 未列出 GPU：只说明当前视角未枚举到，应核对宿主身份、设备暴露和平台资产，
  不能从 VM / 容器结果推断物理机无 GPU。
- `nvidia-smi` 报错：区分工具、驱动、权限、设备可见性和硬件状态，不统称“无 GPU”；
  它也不用于判断非 NVIDIA GPU。
- 验收记录入口、target / host、session、身份依据、命令退出码和实际输出；
  访问成功与硬件结论分别报告。无 GPU 的物理机也可以访问成功，证据不足则标明未确认。

## 4. 仅在明确要求新接入时安装

确需长期接入且获安装授权后，按 [集群接入指南](CLUSTER_INSTALL_GUIDE.md) 的
Kubernetes / K3s Helm 流程执行，使用仓库 `backend/scripts/release-managed-agent.sh`
与 `backend/deploy/helm/doops-agent`，准备已审查 values、授权 kubeconfig 和已核验
CI 镜像 digest，并完成指南中的安装验收。新集群 bootstrap 不代表已完成 Gateway
接管验收，也不提供自动回滚。

本文不提供独立 Docker 高权限安装配方，不宣称 ESXi 可以直接运行 Linux Agent 容器。
没有符合指南的运行环境时，先确认受支持的接入方案；不能为“查看硬件”擅自安装
Docker、K3s、驱动或新 Agent。
