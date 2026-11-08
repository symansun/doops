import type { AgentAttachmentReference } from "./contracts.js";
export type AgentQueuePriority = "now" | "next" | "later";
export type AgentQueueItemStatus = "queued" | "dispatching" | "failed" | "cancelled";
export interface AgentQueuedMessage {
    commandId: string;
    text: string;
    attachments: readonly AgentAttachmentReference[];
    priority: AgentQueuePriority;
    status: AgentQueueItemStatus;
    createdAt: string;
    error?: string;
}
export interface AgentTaskQueueView {
    items: readonly AgentQueuedMessage[];
    pendingCount: number;
    dispatchingCount: number;
}
export declare class AgentTaskQueue {
    private readonly items;
    private sequence;
    private readonly order;
    enqueue(input: {
        commandId: string;
        text: string;
        attachments?: readonly AgentAttachmentReference[];
        priority?: AgentQueuePriority;
        createdAt?: string;
    }): void;
    next(): AgentQueuedMessage | undefined;
    markDispatching(commandId: string): void;
    fail(commandId: string, error: unknown): void;
    remove(commandId: string): void;
    update(commandId: string, patch: {
        text?: string;
        attachments?: readonly AgentAttachmentReference[];
    }): boolean;
    retry(commandId: string): boolean;
    has(commandId: string): boolean;
    clear(): void;
    cancel(commandId: string): boolean;
    project(): AgentTaskQueueView;
    private patchItem;
}
