import type { WorkerRecovery } from "../contracts.js";
export type WorkerReadOnlyReason = "permission" | "closed-subagent" | "native-subagent" | "control" | "paused" | "disconnected" | "ended";
export type WorkerUnreachable = {
    reason: "launch-failed";
    detail: string | null;
} | {
    reason: "orphaned";
} | {
    reason: "status-unavailable";
    detail: string | null;
} | {
    reason: "stranded";
} | {
    reason: "session-unresolved";
    detail: string | null;
} | {
    reason: "session-not-created";
} | {
    reason: "forbidden";
};
export type WorkerLiveness = {
    state: "unknown";
} | {
    state: "starting";
    progress: string | null;
} | {
    state: "online";
    readOnly: WorkerReadOnlyReason | null;
} | {
    state: "unreachable";
    cause: WorkerUnreachable;
    recovery: WorkerRecovery[];
};
export declare function isWorkerSessionReadable(liveness: WorkerLiveness): boolean;
export declare function sameWorkerLiveness(left: WorkerLiveness, right: WorkerLiveness): boolean;
