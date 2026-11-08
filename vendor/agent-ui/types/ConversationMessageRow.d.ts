import type { AgentTimelineItem } from "./contracts.js";
import type { AgentContentRendererRegistration } from "./react/contentRendererTypes.js";
import type { ContentRendererRegistry } from "./registry/ContentRendererRegistry.js";
import type { AgentSessionRuntime } from "./contracts.js";
type MessageItem = Extract<AgentTimelineItem, {
    kind: "message";
}>;
export declare function ConversationMessageRow({ contentRenderers, item, runtime, sessionId }: {
    contentRenderers?: ContentRendererRegistry<AgentContentRendererRegistration>;
    item: MessageItem;
    runtime?: AgentSessionRuntime;
    sessionId?: string;
}): import("react/jsx-runtime").JSX.Element;
export {};
