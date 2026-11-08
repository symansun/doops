import type { SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
export type AgentSessionResyncReason = "base_revision_mismatch" | "buffer_overflow" | "digest_conflict" | "history_visibility_changed" | "manual_reconnect" | "sequence_gap" | "stream_epoch_changed" | "transport_reconnect";
export interface AgentSessionState {
    snapshot: SessionSnapshot;
    status: "ready" | "resync_required";
    resyncReason?: AgentSessionResyncReason;
    appliedBatches: ReadonlyMap<string, string>;
}
export declare class AgentSessionReductionError extends Error {
    readonly code: string;
    constructor(code: string);
}
export declare function isSnapshotCursorConflict(cause: unknown): boolean;
export declare function batchIdentity(input: {
    streamEpoch: string;
    revision: bigint;
    firstSequence: bigint;
    lastSequence: bigint;
}): string;
