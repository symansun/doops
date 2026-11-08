# @agent-cloud/agent-ui

React 对话组件与 TypeScript 会话运行时。宿主项目直接 import 组件，通过接口对接后端；不需要 iframe、嵌入页或 Java/JAR。

## 安装和使用

```bash
npm install @agent-cloud/agent-ui
```

支持 React 18.2/19、现代浏览器和 ESM 构建工具。对话 UI 运行在浏览器中；Next.js 宿主在 client component 中装配 runtime。React 为 peer dependency，不随包重复打包。

```tsx
import { useMemo } from "react";
import { AgentWorkspace } from "@agent-cloud/agent-ui";
import {
  AgentWorkbenchConnectTransport,
  createAgentWorkbenchRuntime,
} from "@agent-cloud/agent-ui/runtime";
import "@agent-cloud/agent-ui/styles.css";

export function Conversation({ baseUrl, tenantId, sessionId, getAccessToken }) {
  const runtime = useMemo(() => createAgentWorkbenchRuntime({
    sessionId,
    title: "AI 助手",
    agentLabel: "AI 助手",
    interactionMode: "acp",
    transport: new AgentWorkbenchConnectTransport({
      baseUrl, tenantId, sessionId, getAccessToken,
    }),
  }), [baseUrl, tenantId, sessionId, getAccessToken]);

  return (
    <div className="agent-ui" style={{ height: 600 }}>
      <AgentWorkspace runtime={runtime} sessionId={sessionId} locale="zh-CN" />
    </div>
  );
}
```

`getAccessToken` 返回调用者当前的用户 bearer，可同步或异步。保持该函数引用稳定，避免每次渲染重建 runtime。组件挂载时打开会话，卸载时关闭连接；关闭连接不删除会话、不停止远端 Worker。

已有会话直接传入 sessionId。创建/复用伙伴会话属于宿主接口接入层：可用当前用户身份调用 Agent Cloud 已有伙伴 session 接口，再将返回的 sessionId 传入组件。tenantId 为 AMP 数字租户 ID；API Key 不能代替 Workbench 用户 bearer。建议宿主 BFF 代理 `/proto.*` 与所需 Session REST，跨域直连时需配置后端 CORS。

## 两种接口接入方式

1. **连接 Agent Cloud**：使用上例的 Connect transport 和 runtime。它复用现有快照、增量流、命令回执、审批、配置、工具动作和表单扩展协议，包含运行时重连与快照恢复逻辑。
2. **连接宿主自有后端**：实现 `AgentSessionRuntime` 接口，传给 `AgentWorkspace`。宿主提供 `open/close/getSnapshot/subscribe/sendMessage/interrupt/resolvePermission/updateConfiguration/loadOlder`；无需依赖 Agent Cloud 登录页、Pod store 或 Relay 连接。

宿主后端可以直接复用共享实现，不必重写 runtime：实现
`AgentWorkbenchSessionTransport` 的 `getSnapshot/streamDeltas/execute`，然后传给
`createAgentWorkbenchRuntime`。Node BFF 可从 `@agent-cloud/agent-ui/server`
导入 `AgentWorkbenchSessionSource` 生成同一 v2 协议；它复用现有 store/reducer，
支持真实 revision/sequence/digest、有界重放和命令去重。宿主负责鉴权、原生事件
投影以及权威执行/取消回执；source 不把收到命令或关闭连接推断为任务成功。
`./server` 只供 Node 服务端使用，不要导入浏览器 bundle。该导出随 `0.1.4`
提供；调用 `emit(events, streamEpoch)` 时必须保留生产者启动时的原始 epoch，
不能在异步任务结束后读取当前 epoch 补写旧事件。

输入区只有在连接完成且 command descriptor/actions 与本会话 grant 均允许发送时
才可用。`interactionMode=acp` 本身不赋予发送权限，也不会在连接恢复中放开输入。

`getSnapshot` 必须在状态未变化时返回同一对象；状态变化后更新对象并通知订阅者。真实后端能力要与快照 capabilities 一致，不支持的操作不要声明可用。

## 宿主 composer 插槽

`AgentWorkspace` 的 `composer?: ReactNode` 可用于 DoOps 结构化部署表单。省略或传入 `undefined` 时使用内置自由文本 composer；传入其他 ReactNode 时替换内置 composer；传入 `null` 时隐藏输入区。空对话和已有消息的对话均支持该插槽，时间线、执行进度与审批仍由工作区通过 runtime/snapshot 展示。

```tsx
<AgentWorkspace
  runtime={runtime}
  sessionId={sessionId}
  presentation="user"
  readOnly={readOnly}
  composer={
    <DeploymentForm
      disabled={readOnly || !canDeploy || submitting}
      onSubmit={submitDeployment}
    />
  }
/>;
```

`DeploymentForm` 与 `submitDeployment` 由宿主实现：宿主负责业务权限校验、参数验证、提交、错误反馈及提交中/禁用状态，并将执行结果接回同一会话 runtime。工作区不会自动为自定义节点绑定 `sendMessage`、禁用表单或补充内置 composer 的模型选择、附件和中断控件；`readOnly` 也不会自动禁用宿主节点，宿主必须同步约束自己的提交入口。会话审批继续由 `ApprovalDock` 根据 runtime capabilities 处理。

完整 runtime 接入见 [React 宿主示例](./examples/react/src/main.tsx)；宿主在其 `AgentWorkspace` 上按上例传入自己的表单即可。

## 可插拔 Agent 适配器

需要同时支持多个模型 Agent 时，宿主实现 `AgentAdapter`，把会话 descriptor、工具/内容 renderer 和宿主能力放在一个对象中。将它传给 `AgentWorkspace` 或 `WorkerTransport.adapterForSession`，组件会自动使用 adapter descriptor，并把 adapter renderer 与宿主 registry 合并；同一个 renderer key 重复注册会直接报错，避免不同客户端静默覆盖。

```tsx
const adapter = {
  descriptor: createAgentHarnessDescriptor({
    id: "codex",
    displayName: "Codex",
    profile: "hybrid",
    protocol: "acp",
    capabilities: { artifacts: true, permissions: true },
  }),
  renderers: {
    tools: [{ key: codexToolKey, renderer: codexToolRenderer }],
  },
};

<AgentWorkspace adapter={adapter} runtime={runtime} sessionId={sessionId} />;
```

adapter 只负责客户端差异；会话状态、执行步骤、命令回执、审批和附件仍通过统一 `AgentSessionSnapshot` 与 `AgentSessionRuntime` 传递。普通消息在 Agent 执行中会进入内存任务队列，执行结束后按优先级逐条发送，队列项可取消、编辑、重试和移除。

附件上传与历史预览分开建模：宿主实现 `uploadAttachment` 和 `loadAttachment` 时，消息中的图片会直接读取 session file 内容；artifact 的 `loadArtifact` 只处理有 descriptor 和授权的产物。

Worker 宿主需要处理 Agent 自有协议时，可从 `WorkerClient` 获取同一会话的桥接器。桥接器会校验 command catalog，显式调用 adapter 的 command/event projection；缺少 projection 或命令不在 catalog 中会返回错误，避免不同客户端静默降级。

```ts
const bridge = workerClient.adapterBridgeFor(workerRef, sessionId);
const wireCommand = bridge?.projectCommand(command);
const event = bridge?.decodeEvent(rawEvent);
const view = bridge?.projectEvent(rawEvent);
```

React 宿主也可以在 `WorkerConversation` 的 `onAdapterBridgeReady` 中接收同一桥接器；会话断开或切换时回调会收到 `undefined`。

## 组件和协议入口

| 导出 | 用途 |
| --- | --- |
| `@agent-cloud/agent-ui` | AgentWorkspace、会话页签、审批/工具/制品视图、渲染注册表、UI 类型 |
| `@agent-cloud/agent-ui/runtime` | Connect transport、会话 store/reducer、命令与运行时 |
| `@agent-cloud/agent-ui/protocol` | 同一 Proto 生成的消息类型与 schema |
| `@agent-cloud/agent-ui/worker` | WorkerClient、宿主 transport 契约、恢复和存活状态组件 |
| `@agent-cloud/agent-ui/styles.css` | 预编译样式；只作用于 `.agent-ui` 范围 |

协议实现和类型声明包含在发布包内，安装方无需安装私有 `@agent-cloud/proto`、配置 monorepo 路径或编译包内 TSX。runtime/protocol 可单独导入；UI、PDF、终端等浏览器能力按自身入口使用。

## 样式和可选能力

宿主无需安装 Tailwind。导入一次 styles.css，并使用 `.agent-ui` 容器；也可以将该类加在独立组件本身的 className 上。样式提供默认主题，读取宿主 background/foreground/primary 等 CSS 变量，可用 `--agent-ui-background`、`--agent-ui-foreground`、`--agent-ui-primary` 覆盖主色。暗色外观通过宿主主题变量提供。

附件上传与受鉴权制品读取需要在 `createAgentWorkbenchRuntime` 注入 `uploadAttachment`、`createArtifactLoader`/`loadPreviewUrl`。宿主在回调中调用自己的 BFF；`createAgentArtifactLoader` 提供现成的制品表示与授权检查。完整终端需要单独提供 `terminalRuntime`；代码工作台需要 `codeWorkspace`。这些能力不会因安装 UI 包自动连接到宿主私有资源。

图片灯箱的 body portal 自带 `.agent-ui` 作用域；也可以通过 `ImageLightboxProvider.portalContainer` 指定宿主容器。富内容仍可能使用受限 iframe 渲染 HTML 制品，这与通过 iframe 嵌入整个对话应用是不同层次。

## DoOps workspace integration

This vendored 0.1.4 distribution is installed by the repository workspace. Its license
is preserved in `LICENSE`; see the root `THIRD_PARTY_NOTICES.md` for local changes.
The public source and installation entrypoint are maintained at
https://github.com/symansun/doops.
