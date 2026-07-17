# doops-agent WebSocket API 协议指南

本文档说明 `doops-agent` 本地 WebSocket 协议，仅用于首次自举、Agent
自恢复或底层协议测试。日常操作必须使用带 `-session` 的 DoOps CLI，
通过 Gateway target 进入目标 Agent；不得把 Agent IP、端口或直连 token
登记为标准 target，也不得把本协议当作 Gateway 的 fallback。

## 1. 基础概念

doops-agent 采用 **MCP (Model Context Protocol)** 风格的 JSON-RPC 消息，通过 **WebSocket** 实现双向通信。

-   **鉴权**: 本地调试握手必须包含 `X-Doops-Key` Header，其值从 Agent 运行时 Secret 注入，不写入命令历史或文档。
-   **通信模型**: 客户端连接 `/ws` 后，在同一条 WebSocket 连接上发送 JSON-RPC 请求并接收流式响应。
-   **标准远程入口**: Gateway 负责用户鉴权、cluster/instance 路由、在线状态和操作审计；调用方使用 `doops -session <s> ... --target <gateway-target>`。

## 2. 交互流程

### 步骤 1: 建立 WebSocket 连接
仅在 Agent 所在主机或 Pod 网络命名空间内连接
`ws://127.0.0.1:42222/ws`。禁止从日常客户端直接连接节点 IP。

**请求示例**:
```bash
websocat -H "X-Doops-Key: ${DOOPS_AGENT_TOKEN}" ws://127.0.0.1:42222/ws
```

### 步骤 2: 发送 JSON-RPC 请求
向 WebSocket 连接发送 JSON-RPC 请求。

**通用格式**:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "工具名称",
    "arguments": { ... }
  }
}
```

### 步骤 3: 接收响应
响应会通过同一条 **WebSocket** 连接回传。

-   **中间状态**: `method: "notifications/message"` (用于 PTY 增量输出流)。
-   **最终结果**: `id: [请求ID]` (包含执行结果文本)。

## 3. 核心 API 工具

### 3.1 提交自然语言任务 (AI 自动运维)
这是最强大的接口，会自动进行：分析 -> 翻译命令 -> 执行 -> 验证结果。

-   **工具名**: `doops_agent_prompt`
-   **参数**:
    -   `instruction`: (string) 自然语言指令。
    -   `session_id`: (string, 可选) 维持 PTY 状态的 ID。

**示例**:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "doops_agent_prompt",
    "arguments": {
      "instruction": "检查 /var/log/nginx 下是否有 error 关键字，并统计行数"
    }
  }
}
```

### 3.2 直接执行 Shell 命令
-   **工具名**: `doops_shell`
-   **参数**:
    -   `command`: (string) 要执行的命令。
    -   `session_id`: (string, 可选) 默认 "default"。

### 3.3 文件操作
-   **写入**: `doops_file_write` (path, content, session_id)
-   **查看小文本**: `doops_file_read` (path, session_id)
-   **拉取工作区**: `doops pull`

`doops_file_read` 只用于查看配置、脚本、日志片段等小文本文件。大文件、压缩包、图片、数据库导出等二进制资产不能通过 `read` 回收，避免把 WebSocket 文本响应误用成下载通道。
大文件和二进制资产应使用 `doops pull`，其底层基于远端工作区 Git 快照和 Git HTTP。

### 3.4 仅翻译指令 (不执行)
-   **工具名**: `doops_shell_gen`
-   **参数**: `instruction`

## 4. 示例代码 (Node.js WebSocket)

```javascript
import WebSocket from 'ws';

const ws = new WebSocket('ws://127.0.0.1:42222/ws', {
  headers: { 'X-Doops-Key': process.env.DOOPS_AGENT_TOKEN },
});

ws.on('open', () => {
  ws.send(JSON.stringify({
    jsonrpc: '2.0',
    id: 'task-1',
    method: 'tools/call',
    params: {
      name: 'doops_agent_prompt',
      arguments: { instruction: 'uptime' },
    },
  }));
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  console.log(msg);
});
```
