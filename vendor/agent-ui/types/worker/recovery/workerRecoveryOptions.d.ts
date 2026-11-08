import type { WorkerRecovery } from "../contracts.js";
import type { WorkerUnreachable } from "../liveness/workerLiveness.js";
export interface RecoveryContext {
    sessionId: string;
    serverUrl: string;
    wrapper: string | null;
    isUnboundFork: boolean;
    sourceHostId: string | null;
}
export declare function recoveryOptionsFor(cause: WorkerUnreachable, ctx: RecoveryContext): WorkerRecovery[];
export declare function isUnboundCodingFork(labels: Record<string, string>, workspace: string | null | undefined): boolean;
