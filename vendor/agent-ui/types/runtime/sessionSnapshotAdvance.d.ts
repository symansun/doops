import type { SessionSnapshot } from "../generated/agent_workbench/v2/session_pb.js";
export declare function validateSnapshotAdvance(current: SessionSnapshot, next: SessionSnapshot): "advance" | "identical" | "metadata";
