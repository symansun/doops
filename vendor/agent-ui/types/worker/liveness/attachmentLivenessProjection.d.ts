import type { SessionAttachmentFailure } from "../session/sessionAttachment.js";
import type { WorkerLiveness } from "./workerLiveness.js";
export declare function projectAttachmentFailure(failure: SessionAttachmentFailure, detail: string): WorkerLiveness;
