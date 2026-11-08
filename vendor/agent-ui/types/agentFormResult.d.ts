import type { AgentFormFieldIssue, AgentFormSurfaceDocument } from "./agentFormSurface.js";
export interface AgentExtensionResult {
    status: "accepted" | "succeeded";
}
export interface AgentFormSavedResult {
    commandId: string;
    actionId?: string;
    message: string;
    submittedData: Record<string, unknown>;
    notificationStatus?: string;
}
export interface AgentFormState {
    document: AgentFormSurfaceDocument;
    result: AgentFormSavedResult;
}
export declare function formCommandFailure(error: unknown): {
    fields: AgentFormFieldIssue[];
    message: string;
    terminal: boolean;
};
