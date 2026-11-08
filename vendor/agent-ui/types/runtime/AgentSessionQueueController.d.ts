import type { AgentAttachmentReference } from "../contracts.js";
import type { AgentQueuePriority, AgentTaskQueue } from "../agentTaskQueue.js";
import type { AgentSessionConnection } from "./AgentSessionConnection.js";
import { PendingPromptProjection } from "./pendingPromptProjection.js";
import { SessionFrameNotifier } from "./sessionFrameNotifier.js";
import { sendAgentPrompt } from "./sendAgentPrompt.js";
export type AgentQueueCommandProgress = {
    status: "pending";
} | {
    status: "complete";
} | {
    status: "failed";
    error: string;
};
export interface AgentSessionQueueControllerOptions {
    connection: AgentSessionConnection;
    notifier: SessionFrameNotifier;
    pending: PendingPromptProjection;
    sessionId: string;
    queue: AgentTaskQueue;
    execute: Parameters<typeof sendAgentPrompt>[0]["execute"];
    getGeneration: () => number;
    isCurrent: (generation: number) => boolean;
    isClosed: () => boolean;
    getCommandProgress: (commandId: string) => AgentQueueCommandProgress;
    canDispatchSession: () => boolean;
    refresh: () => void;
}
export declare class AgentSessionQueueController {
    private readonly options;
    private draining;
    private awaitingCommandId?;
    private dispatchToken;
    constructor(options: AgentSessionQueueControllerOptions);
    enqueue(input: {
        commandId: string;
        text: string;
        attachments?: readonly AgentAttachmentReference[];
        priority: AgentQueuePriority;
    }): void;
    cancel(commandId: string): boolean;
    update(commandId: string, input: {
        text?: string;
        attachments?: readonly AgentAttachmentReference[];
    }): boolean;
    retry(commandId: string): boolean;
    remove(commandId: string): boolean;
    clear(): void;
    trigger(): void;
    private drain;
    private canDispatch;
}
