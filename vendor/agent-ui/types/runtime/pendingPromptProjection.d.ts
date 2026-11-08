import type { SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
import type { AgentAttachmentReference, AgentSessionSnapshot } from "../contracts.js";
export interface PendingPrompt {
    epoch: string;
    text: string;
    attachments?: AgentAttachmentReference[];
    failed?: boolean;
}
export declare class PendingPromptProjection {
    private readonly prompts;
    add(commandId: string, prompt: PendingPrompt): void;
    fail(commandId: string): void;
    remove(commandId: string): void;
    clear(): void;
    project(view: AgentSessionSnapshot, source?: SessionSnapshot): AgentSessionSnapshot;
}
