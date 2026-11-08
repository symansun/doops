import type { AgentWorkspaceLocale } from "./agentWorkspaceText.js";
import type { WorkerReadOnlyReason } from "./worker/liveness/workerLiveness.js";
export declare function workerSessionRecoveryHint(reason: WorkerReadOnlyReason | null | undefined, locale: AgentWorkspaceLocale): string | null;
