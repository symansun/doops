# 物理机 / 宿主机 / Bare-metal 接入指南

> 这是"doops 看不到物理机硬件"的真正原因与正确路径。doops 不是缺陷，
> 它只能执行 **Agent 进程所在 OS 用户态** 的命令；Agent 跑在 Proxmox VM 内部，
> 就看不到宿主 GPU/PCIe 设备。要看真硬件，必须让 Agent 直接跑在物理机/宿主机用户态。

## 1. 为什么 doops 看不到物理机硬件

- doops Agent 是以 **容器** 形式运行的（详见 `docs/CLUSTER_INSTALL_GUIDE.md`）。
  容器看到的设备 = 容器被授予的设备 + 宿主机 OS 能看到的设备。
- 当 Agent 跑在 Proxmox/ESXi/KVM/Xen 虚拟机内时，VM 透传什么设备，
  容器就只能看到什么：
  - 默认 Proxmox VM 看到的是 `virtio_gpu`、虚拟 CPU、虚拟磁盘，根本没有真显卡。
  - 即便 Proxmox 配置了 PCIe passthrough / vGPU，VM 内只会看到宿主显式分配出来的设备。
  - 真实物理机的 GPU、NVMe、PCIe 拓扑对 VM 是不可见的。
- doops 不替代 Proxmox API / IPMI / iLO / Redfish 等带外管理面，
  不能"反弹出"宿主机执行命令。

> 这不是 SKILL 的缺陷，而是部署形态决定的边界。请按下面三条路径之一解决。

## 2. 三条真实接入路径（按推荐顺序）

### 路径 A：在物理机/宿主机上跑 doops Agent 容器（最简、最推荐）

适用：物理机、Proxmox 宿主机、ESXi 宿主机、bare-metal 服务器。

核心做法：把 Agent 容器直接部署在物理机/宿主机 OS 上，而不是部署在 VM 内。
这样容器内 `lspci`、`nvidia-smi` 看到的就是物理机的真实硬件。
完整步骤见 `docs/CLUSTER_INSTALL_GUIDE.md` 模式二（纯 Docker 单机环境）。

步骤：

1. 登录到物理机/宿主机 OS 用户态（不是 VM 内部）。
   - 物理机：本机控制台、IPMI/iLO/Redfish 远程 console，或已经能 SSH 到它。
   - Proxmox 宿主：从 Proxmox Web UI 点 "Shell"，或 SSH 到宿主 IP。
2. 在宿主机上安装 `docker`（必须；如果是 K8s/K3s 集群走模式一）。
3. 准备 Agent 启动参数：
   - `gateway-url`：Gateway 外网入口。
   - `cluster` / `instance`：唯一标识这台物理机，例如 `cluster=edu-baremetal`、
     `instance=host-01`。
   - `doagent-model-settings.json`：doagent 配置文件 Secret。
4. 启动 Agent 容器（与 `CLUSTER_INSTALL_GUIDE.md` 模式二一致）：
   ```bash
   docker run -d \
     --name doops-agent \
     --net host --pid host --ipc host --privileged \
     -v /run/containerd:/run/containerd \
     -v /var/lib/containerd:/var/lib/containerd \
     -v /var/lib/nerdctl:/var/lib/nerdctl \
     -v /root/.kube/config:/root/.kube/config:ro \
     -v /path/to/doagent-model-settings.json:/opt/doagent_config/settings.json:ro \
     docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest-from-successful-CI> \
     -gateway-url https://gateway.example.com \
     -cluster <physical-cluster> \
     -instance <physical-host-name>
   ```
5. 在已登录 gateway 的机器上执行：
   ```bash
   doops targets --target <gateway-target>
   ```
   列表里会多出 `<physical-cluster>/<physical-host-name>` 一行。
6. 验证是物理机身份（不是 VM）：
   ```bash
   doops -session <s> exec --target <physical-cluster/physical-host> -- hostname -I
   doops -session <s> exec --target <physical-cluster/physical-host> -- lspci | grep -i nvidia
   doops -session <s> exec --target <physical-cluster/physical-host> -- nvidia-smi -L
   ```
   - `hostname -I` 必须是物理机/宿主机的真实 IP（与 VM 内 IP 不同）。
   - `lspci` 看到真显卡（或 `nvidia-smi` 成功）才算成功。

### 路径 B：把已装 Agent 的物理机注册为独立 target

适用：物理机在远端机房、运维人员无法直接 console/SSH，但 Agent 在启动后
能反向连到 gateway。

步骤：

1. 在物理机/宿主机上按路径 A 装好 Agent 容器。
2. Agent 启动时只要 `gateway-url` 可达，container 会主动反向注册到 gateway。
   **不要**额外写 `doops admin target register；如果 gateway 上看不到这条 target，
   先用 `doops targets --target <gateway-target>` 排查反向隧道（见
   `docs/DOOPS_GATEWAY_TUNNEL.md`）。**
3. 跨网络场景（物理机在外网/无法被 gateway 直连）：
   - 保持 Agent 主动反向连出 gateway 即可。
   - Gateway 端需要给该 cluster/instance 签发 agent registration 凭据。
   - 不要把凭据写到 prompt / chat。

### 路径 C：借已注册 VM target 跳板 SSH 到物理机（fallback，不推荐长期）

适用：物理机无法直接装 Agent 容器，但和某个已注册 VM Agent 在同一内网。

步骤：

1. 在 VM Agent 上验证确实能 SSH 到物理机：
   ```bash
   doops -session <s> exec --target <vm-target> -- \
     ssh -o StrictHostKeyChecking=accept-new user@<physical-ip> hostname -I
   ```
2. 物理机必须开启 `sshd` 并允许来自该 VM 的 key 登录。
3. 凭据通过 `doops credential create / put / grant` 注入到 VM target，不要在命令行内联密码。
4. 后续用 `doops ssh --target <vm-target> --host user@<physical-ip> -- <cmd> [args...]`
   跳板执行；多层命令使用 `bash -lc '<script>'` 作为一个远端 argv。

限制：

- 物理机必须有 sshd。
- 跳板多一跳，命令延迟和审计复杂度都增加。
- 验收仍然要 `lspci` 看到真显卡或 `nvidia-smi` 成功。

## 3. 三条路径都不行时的禁止动作

- 不要让 Agent 尝试通过 Proxmox API/IPMI 直接读写宿主硬件（除非有人手动配置并授权）。
- 不要在 chat 里发密码 / SSH 私钥 / token。
- 不要把物理机/宿主机误解为 K8s 节点：doops 列表里的 "node" = VM/物理机 OS，
  不是 K8s Node；K8s Node 是否能看到 GPU 是另一回事（要装 NVIDIA Device Plugin）。

## 4. 验证清单（机器人/AI 必跑）

- [ ] `hostname -I` 与目标物理机/宿主 IP 一致。
- [ ] `lspci | grep -i nvidia` 看到真显卡（或 `nvidia-smi` 成功）。
- [ ] `nvidia-smi -L` 与 BIOS 报告的 GPU 数量一致。
- [ ] `doops targets --target <gateway>` 列表里出现该物理机 target，且 `busy=false`。
- [ ] 当前 session 有审计 trail（`doops admin operations list --target <gateway>`）。

## 5. 对话意图硬规则

- 用户说 "可以进入物理机 / 已经在宿主 / 拿到宿主机 shell / IPMI 已通" 任意一项时，
  立刻按路径 A 给 `docker run ... doops-agent@sha256:...`，
  **不要纠缠 SSH 凭据、不要假设已有 agent**。
- "如何进入物理机" 类型的提问，回答固定为本文路径 A 的启动步骤；
  路径 B/C 仅在 A 不可行（远端机房 / 物理机无法装 Agent）时再讨论。
- 当前对话上下文（edu-coder → Proxmox → 物理机）下，目标判定为：
  - 物理机是否已暴露 SSH/IPMI/console？若是 → 路径 A。
  - 物理机只暴露 Proxmox Web UI 8006？→ 仍走路径 A，先在宿主 OS 用户态跑 docker。
  - 物理机全部资产都封闭在 VM 内、宿主无法登录？→ 这是真的"doops 够不到"，
    必须升级给运维人员处理，不要在 Agent 内自创 fallback。
