import type { AgentWorkspaceLocale } from "../../agentWorkspaceText.js";
import type { WorkerLiveness, WorkerUnreachable } from "../liveness/workerLiveness.js";
export interface WorkerLivenessCopy {
    starting: string;
    reasons: Record<WorkerUnreachable["reason"], string>;
    reconnectCli: string;
    retry: string;
}
export declare const WORKER_LIVENESS_COPY: Record<AgentWorkspaceLocale, WorkerLivenessCopy>;
export declare function workerLivenessMessage(liveness: WorkerLiveness, copy: WorkerLivenessCopy): string;
export declare function workerLivenessDetail(liveness: WorkerLiveness): string | null;
