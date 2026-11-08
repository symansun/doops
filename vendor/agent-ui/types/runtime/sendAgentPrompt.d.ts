import type { AgentAttachmentReference } from "../contracts.js";
import { sendPromptPayload } from "./agentSessionCommandPayloads.js";
import type { AgentSessionConnection } from "./AgentSessionConnection.js";
import { PendingPromptProjection } from "./pendingPromptProjection.js";
import { SessionFrameNotifier } from "./sessionFrameNotifier.js";
export declare function sendAgentPrompt(options: {
    connection: AgentSessionConnection;
    commandId: string;
    execute: (sessionId: string, commandId: string, payload: ReturnType<typeof sendPromptPayload>) => Promise<unknown>;
    generation: number;
    input: {
        text: string;
        attachments?: AgentAttachmentReference[];
    };
    isCurrent: (generation: number) => boolean;
    notifier: SessionFrameNotifier;
    pending: PendingPromptProjection;
    sessionId: string;
}): Promise<void>;
