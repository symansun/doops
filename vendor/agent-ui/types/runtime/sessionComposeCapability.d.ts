import type { AgentConnectionStatus } from "../contracts.js";
export declare function canComposePrompt(input: {
    connection: AgentConnectionStatus;
    hasSendPrompt: boolean;
    interactionMode: "acp" | "pty";
}): boolean;
