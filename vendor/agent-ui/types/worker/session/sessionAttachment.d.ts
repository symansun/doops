export type SessionAttachmentFailure = "not-created" | "forbidden" | "load-failed";
export declare class SessionAttachmentError extends Error {
    readonly failure: SessionAttachmentFailure;
    constructor(failure: SessionAttachmentFailure, message: string);
}
export declare function sessionAttachmentFailureOf(error: unknown): SessionAttachmentFailure;
