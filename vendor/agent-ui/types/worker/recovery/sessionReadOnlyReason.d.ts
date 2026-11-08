import type { WorkerReadOnlyReason } from "../liveness/workerLiveness.js";
export declare function sessionReadOnlyReason(labels: Record<string, string>, permissionLevel: number | null | undefined): WorkerReadOnlyReason | null;
