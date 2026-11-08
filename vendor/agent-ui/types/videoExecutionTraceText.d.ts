import type { UserVideoExecutionDetail, UserVideoExecutionStepId } from "./userVideoExecutionTrace.js";
export interface VideoExecutionTraceText {
    detail: Record<Exclude<UserVideoExecutionDetail, "rendering">, string>;
    label: string;
    rendering(progress?: number): string;
    step: Record<UserVideoExecutionStepId, string>;
}
export declare const englishVideoExecutionTrace: VideoExecutionTraceText;
export declare const chineseVideoExecutionTrace: VideoExecutionTraceText;
