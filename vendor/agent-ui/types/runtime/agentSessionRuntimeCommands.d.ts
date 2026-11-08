import type { AgentSessionRuntime } from "../contracts.js";
import type { AgentSessionConnection } from "./AgentSessionConnection.js";
import type { AgentInteractionExecutor } from "./AgentInteractionExecutor.js";
import type { PendingPromptProjection } from "./pendingPromptProjection.js";
import type { SessionFrameNotifier } from "./sessionFrameNotifier.js";
import type { executeSessionCommand, SessionCommandPayload } from "./executeSessionCommand.js";
interface RuntimeCommandContext {
    connection: AgentSessionConnection;
    interactionExecutor: AgentInteractionExecutor;
    notifier: SessionFrameNotifier;
    pending: PendingPromptProjection;
    getGeneration: () => number;
    isCurrent: (generation: number) => boolean;
    requireSession: (id: string) => void;
    execute: (sessionId: string, commandId: string, payload: SessionCommandPayload) => ReturnType<typeof executeSessionCommand>;
}
type RuntimeCommands = Pick<Required<AgentSessionRuntime>, "sendMessage" | "sendSlashCommand" | "interrupt" | "resolvePermission" | "updateConfiguration" | "executeArtifactAction">;
export declare function createAgentSessionRuntimeCommands(context: RuntimeCommandContext): RuntimeCommands;
export {};
