# DoOps 代码同步方案

> [!IMPORTANT]
> 本文记录代码同步方案的收敛结果，不是多路径兼容指南。标准路径只有
> `doops -session <session> push --target <gateway-target> --src <dir>`：
> CLI 通过 Gateway Git HTTP 隧道把工作区同步到目标 Agent 的
> `/root/ws/<session>`。不得使用 tar/base64、`write`、自建 `/upload`
> 端点、rsync 或客户端直连 SSH 作为代码同步替代路径。在线 Agent 仍可通过
> `doops exec` 代理用户明确给出的任意出站 SSH 命令；这不改变 `push/pull`
> 的标准工作区协议。

## 核心问题

用户希望在 agent 远端环境中进行代码构建（buildctl build → push → K8s 部署），需要解决两个关键问题：

1. **代码如何到达 agent 工作目录**
2. **代码变更后如何高效增量同步**

---

## 现有能力盘点

| 能力 | 工具/方式 | 限制 |
|:---|:---|:---|
| 工作区上传 | `doops -session <s> push --target <gateway-target> --src <dir>` | 标准路径，基于 Gateway Git HTTP |
| 工作区下载 | `doops -session <s> pull --target <gateway-target> --dest <dir>` | 标准路径，基于 Gateway Git HTTP |
| 单文件写入 | `doops -session <s> write ...` | 仅用于小文本，不用于源码树或二进制 |
| 小文本查看 | `doops -session <s> read ...` | 仅配置、脚本、短日志等小文本 |
| Shell 执行 | `doops -session <s> exec ...` | 在已同步的固定 session 工作区内执行 |

> [!IMPORTANT]
> 批量文件传输已经由 `doops push/pull` 提供。若出现 512KB 分块、tar、
> base64 或直连 Agent 上传，说明调用方绕过了标准路径，应修复调用方、
> Gateway Git HTTP 隧道或目标 Agent 版本。

---

## 方案对比

```mermaid
graph TD
    A["本地代码"] --> B["doops push"]
    B --> C["Gateway Git HTTP 隧道"]
    C --> D["Agent /root/ws/session"]
    D --> E["buildctl build + push"]
```

### 已淘汰：tar + write 管道

该路径把二进制数据塞进小文本接口，无法提供可靠的工作区身份、增量同步
和 Gateway 审计，禁止使用。

| 维度 | 评价 |
|:---|:---|
| 结论 | 禁止 |
| 原因 | 错用小文本通道，绕过标准工作区协议 |

---

### 标准方案：`doops push` + Gateway Git HTTP

```bash
doops -session myproject-build push \
  --target gw-example \
  --src ./myproject
```

内部流程：
1. CLI 根据 `.gitignore` 和 `.doopsignore` 生成 session 工作区 Git 快照。
2. 通过 `/v1/git/<cluster>/<instance>/<session>.git` 进入 Gateway。
3. Gateway 把 Git HTTP 请求路由到在线且匹配的 Agent。
4. Agent 将工作区固定到 `/root/ws/<session>`。

| 维度 | 评价 |
|:---|:---|
| 可靠性 | Gateway 身份、Agent 路由和 session 隔离均可验证 |
| 效率 | Git 对象增量传输 |
| 适用 | 所有标准 DoOps 工作区同步 |

---

### 已淘汰：rsync over SSH

| 维度 | 评价 |
|:---|:---|
| 结论 | 禁止作为日常同步路径 |
| 原因 | 绕过 Gateway 身份、审计、target 路由和 session 工作区 |

> [!TIP]
> SSH 主机不进入标准 target 配置。需要执行 SSH 命令时，通过已有 Gateway target
> 调用 `doops exec --cmd 'ssh ...'`，由在线 Agent 代理并接受 Gateway 审计。

---

### 发布任务

普通 `push/pull` 仍只走 Gateway Git HTTP。发布不解析仓库中的 environment、
template 或 workflow 声明；调用方通过明确授权的自然语言任务给出 target、服务范围、
镜像版本、原因和验收条件。需要凭据时，CLI 只传递 `--credential` 的资源名，Gateway
在用户、target 和 session 绑定后按精确 scope 授权并物化。

---

## 推荐流程

```mermaid
graph LR
    A["doops push"] --> B["Gateway Git HTTP"]
    B --> C["固定 session 工作区"]
    C --> D["doops exec / ask --apply"]
```

### 同步并构建

```bash
# 1. 同步到固定 session 工作区
doops -session myproject-build push --target gw-example --src .

# 2. 在同一个 session 中构建
doops -session myproject-build exec --target gw-example --cmd \
  "cd /root/ws/myproject-build && buildctl --addr unix:///run/buildkit/buildkitd.sock build \
     --progress=plain \
     --frontend dockerfile.v0 \
     --local context=. \
     --local dockerfile=. \
     --opt filename=Dockerfile \
     --output type=image,name=registry.example.com/oilan-system/myapp:latest,push=true,registry.insecure=true"
```

## 构建工作目录管理

> [!NOTE]
> 代码工作区必须使用 `/root/ws/<session>`。BuildKit 缓存由构建运行时独立
> 管理，不能通过另建共享源码目录破坏 session 隔离。

| 项目 | 路径 | 说明 |
|:---|:---|:---|
| 工作区根目录 | `/root/ws/` | Agent 管理的 session 工作区根目录 |
| 项目工作区 | `/root/ws/<session>/` | `push/pull/exec/ask` 的唯一工作区 |
| 构建日志 | `/tmp/doops-build-{project}.log` | 构建过程日志 |

```bash
/root/ws/
├── exam-lab-api-build/    # session 工作区
│   ├── Dockerfile
│   ├── main.go
│   └── ...
├── go-judge-build/        # 另一个 session
│   └── ...
```

---

## 关键决策点

1. 所有远程命令必须显式携带 `-session`。
2. target 必须是 Gateway target，不登记直连 Agent。
3. 工作区同步只使用 `push/pull` 的 Git HTTP 路径。
4. 路径故障必须修复 Gateway、Agent 或调用方，不设计传输 fallback。
