import type { WorkerReadOnlyReason } from "./worker/liveness/workerLiveness.js";
export type ComposerRecoveryAction = "wake" | "reconnect";
export interface SessionRecovery {
    readOnlyReason?: WorkerReadOnlyReason | null;
    onReconnect?: () => void;
    onWake?: () => void;
}
export declare function composerRecoveryActions(input: {
    canReconnect: boolean;
    canWake: boolean;
    sendMessage: boolean;
    readOnlyReason?: WorkerReadOnlyReason | null;
}): ComposerRecoveryAction[];
export declare function workerComposerRecovery(input: {
    readOnlyReason: WorkerReadOnlyReason | null;
    onReconnect: () => void;
    onWake?: () => void;
}): SessionRecovery;
