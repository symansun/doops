import type { AgentPermissionQuestion } from "./agentPermissionContracts.js";
export declare function questionAnswers(question: AgentPermissionQuestion, selected: string[], custom: string): string[];
export declare function questionAnswered(question: AgentPermissionQuestion, selected: string[], custom: string): boolean;
