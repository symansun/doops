import type { AgentSessionSnapshot } from "./contracts.js";
export interface ConversationProgressState {
    kind: "busy" | "waiting" | "offline";
    title: string;
}
export declare function conversationProgress(snapshot: AgentSessionSnapshot, sending?: boolean): ConversationProgressState | null;
