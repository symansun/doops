import { useAgentWorkspaceText } from "./AgentWorkspaceLocaleContext.js";
import type { AgentSessionRuntime, AgentSessionSnapshot } from "./contracts.js";
type QueueItem = NonNullable<AgentSessionSnapshot["queue"]>["items"][number];
export declare function QueueItemStatusIcon({ status }: {
    status: QueueItem["status"];
}): import("react/jsx-runtime").JSX.Element;
export declare function QueueActions({ editing, item, onCancelEdit, onEdit, onSave, runtime, sessionId, }: {
    editing: boolean;
    item: QueueItem;
    onCancelEdit: () => void;
    onEdit: () => void;
    onSave: () => void;
    runtime: AgentSessionRuntime;
    sessionId: string;
}): import("react/jsx-runtime").JSX.Element | null;
export declare function queueStatus(status: QueueItem["status"], text: ReturnType<typeof useAgentWorkspaceText>): string;
export {};
