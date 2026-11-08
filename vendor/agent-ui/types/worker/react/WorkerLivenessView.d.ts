import type { AgentWorkspaceLocale } from "../../agentWorkspaceText.js";
import type { WorkerLiveness } from "../liveness/workerLiveness.js";
export declare function WorkerLivenessView({ liveness, locale, className, onRetry, }: {
    liveness: WorkerLiveness;
    locale?: AgentWorkspaceLocale;
    className?: string;
    onRetry?: () => void;
}): import("react/jsx-runtime").JSX.Element;
