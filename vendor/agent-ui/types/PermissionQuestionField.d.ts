import type { AgentPermissionQuestion } from "./agentPermissionContracts.js";
interface PermissionQuestionFieldProps {
    custom: string;
    disabled: boolean;
    onCustomChange: (value: string) => void;
    onOptionChange: (label: string) => void;
    question: AgentPermissionQuestion;
    selected: string[];
}
export declare function PermissionQuestionField({ custom, disabled, onCustomChange, onOptionChange, question, selected, }: PermissionQuestionFieldProps): import("react/jsx-runtime").JSX.Element;
export {};
