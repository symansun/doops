import type { AgentAttachmentReference, AgentSessionRuntime, AgentSessionSnapshot } from "../../contracts.js";
import type { MentionItem } from "./mentionSerialize.js";
import { type WorkspaceCodeReference } from "../../workbench/workspaceCodeReference.js";
export declare function submitComposerMessage(options: {
    message: string;
    attachments: AgentAttachmentReference[];
    mentioned: readonly MentionItem[];
    outboundText: string;
    codeReferences: readonly WorkspaceCodeReference[];
    runtime: AgentSessionRuntime;
    snapshot: AgentSessionSnapshot;
    queueWhileActive?: boolean;
    requiresArgument: (label: string) => string;
    slashAttachmentsUnsupported: string;
}): Promise<"sent" | "queued" | "blocked">;
