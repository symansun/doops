export type VideoTaskStatusText = Record<"failed" | "model_quota_exhausted" | "partial" | "processing" | "provider_auth_failed" | "provider_unavailable" | "task_failed" | "verified" | "verification_failed", {
    detail: string;
    title: string;
}>;
export declare const englishVideoTaskStatus: VideoTaskStatusText;
export declare const chineseVideoTaskStatus: VideoTaskStatusText;
