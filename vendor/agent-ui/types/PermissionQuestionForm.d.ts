import type { AgentQuestionPermissionRequest } from "./agentPermissionContracts.js";
interface PermissionQuestionFormProps {
    disabled: boolean;
    permission: AgentQuestionPermissionRequest;
    onReject: () => void;
    onSubmit: (answers: Record<string, string[]>) => void;
}
export declare function PermissionQuestionForm({ disabled, permission, onReject, onSubmit, }: PermissionQuestionFormProps): import("react/jsx-runtime").JSX.Element | null;
export {};
