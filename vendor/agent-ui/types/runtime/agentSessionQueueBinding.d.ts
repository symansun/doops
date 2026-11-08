import type { AgentSessionRuntime, AgentSessionSnapshot } from "../contracts.js";
import type { AgentTaskQueue } from "../agentTaskQueue.js";
import type { AgentSessionConnection } from "./AgentSessionConnection.js";
import { AgentSessionQueueController } from "./AgentSessionQueueController.js";
import { PendingPromptProjection } from "./pendingPromptProjection.js";
import { SessionFrameNotifier } from "./sessionFrameNotifier.js";
import { sendAgentPrompt } from "./sendAgentPrompt.js";
type QueueMethods = {
    enqueueMessage: NonNullable<AgentSessionRuntime["enqueueMessage"]>;
    enqueueSlashCommand: NonNullable<AgentSessionRuntime["enqueueSlashCommand"]>;
    cancelQueuedMessage: NonNullable<AgentSessionRuntime["cancelQueuedMessage"]>;
    updateQueuedMessage: NonNullable<AgentSessionRuntime["updateQueuedMessage"]>;
    retryQueuedMessage: NonNullable<AgentSessionRuntime["retryQueuedMessage"]>;
    removeQueuedMessage: NonNullable<AgentSessionRuntime["removeQueuedMessage"]>;
};
interface AgentSessionQueueBindingOptions {
    connection: AgentSessionConnection;
    notifier: SessionFrameNotifier;
    pending: PendingPromptProjection;
    queue: AgentTaskQueue;
    sessionId: string;
    execute: Parameters<typeof sendAgentPrompt>[0]["execute"];
    getGeneration: () => number;
    isCurrent: (generation: number) => boolean;
    isClosed: () => boolean;
    requireSession: (sessionId: string) => void;
    projectSnapshot: () => AgentSessionSnapshot;
    refresh: () => void;
}
export declare function createAgentSessionQueueBinding(options: AgentSessionQueueBindingOptions): QueueMethods & {
    controller: AgentSessionQueueController;
};
export {};
