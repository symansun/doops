import type { AgentSessionRuntime } from "../../agentSessionRuntime.js";
import type { WorkerClient } from "../WorkerClient.js";
import { type WorkerRef } from "../contracts.js";
import { type WorkerLiveness } from "../liveness/workerLiveness.js";
import { type SessionAttachmentFailure } from "../session/sessionAttachment.js";
export declare function useWorkerLiveness(client: WorkerClient, workerRef: WorkerRef): {
    liveness: WorkerLiveness;
    retry: () => void;
};
export declare function useWorkerSessionId(client: WorkerClient, workerRef: WorkerRef, liveness: WorkerLiveness): {
    sessionId: string | null;
    resolveError: string | null;
    resolveFailure: SessionAttachmentFailure | null;
    retry: () => void;
};
export declare function useWorkerRuntime(client: WorkerClient, workerRef: WorkerRef, sessionId: string | null, liveness: WorkerLiveness): {
    runtime: AgentSessionRuntime | null;
    openError: string | null;
    retryOpen: () => void;
};
