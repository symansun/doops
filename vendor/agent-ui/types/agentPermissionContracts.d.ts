export interface AgentPermissionQuestionOption {
    label: string;
    description: string;
}
export interface AgentPermissionQuestion {
    id: string;
    prompt: string;
    header: string;
    options: AgentPermissionQuestionOption[];
    multiple: boolean;
    allowCustom: boolean;
    secret: boolean;
}
export interface AgentApprovalPermissionRequest {
    id: string;
    kind: "approval";
    title: string;
    description: string;
}
export interface AgentQuestionPermissionRequest {
    id: string;
    kind: "question";
    title: string;
    questions: AgentPermissionQuestion[];
}
export type AgentPermissionRequest = AgentApprovalPermissionRequest | AgentQuestionPermissionRequest;
export interface AgentPermissionAnswerContent {
    answers: Record<string, string[]>;
}
export type AgentPermissionResolution = {
    action: "accept";
    content: AgentPermissionAnswerContent;
} | {
    action: "decline";
};
