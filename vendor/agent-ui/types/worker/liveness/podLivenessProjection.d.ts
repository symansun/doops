import type { WorkerLiveness } from "./workerLiveness.js";
export type PodStatus = "queued" | "initializing" | "running" | "paused" | "disconnected" | "orphaned" | "completed" | "terminated" | "error" | "unknown";
export interface PodLivenessInput {
    podStatus: PodStatus | string;
    isPodReady: boolean;
    initProgress: string | null;
    podError: string | null;
    controlGranted: boolean;
}
export declare function projectPodLiveness(input: PodLivenessInput): WorkerLiveness;
