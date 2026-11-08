import type { ReactNode } from "react";
import type { AgentSessionDeckEntry } from "./AgentSessionDeckTab.js";
import { type WorkerConversationProps } from "./worker/react/WorkerConversation.js";
export interface WorkerConversationDeckEntry extends AgentSessionDeckEntry {
    workerRef: WorkerConversationProps["workerRef"];
}
export interface WorkerConversationDeckProps extends Omit<WorkerConversationProps, "workerRef" | "terminalRuntime" | "workspaceArtifacts" | "showFullscreen"> {
    activeSessionId?: string;
    defaultActiveSessionId?: string;
    onActiveSessionChange?: (sessionId: string) => void;
    onCreateSession?: () => void;
    sessionDeckMode?: "full" | "compact";
    renderSession?: (entry: WorkerConversationDeckEntry, active: boolean) => ReactNode;
    sessions: readonly WorkerConversationDeckEntry[];
}
export declare function WorkerConversationDeck({ activeSessionId, className, defaultActiveSessionId, locale, onActiveSessionChange, onCreateSession, sessionDeckMode, renderSession, sessions, ...conversationProps }: WorkerConversationDeckProps): import("react/jsx-runtime").JSX.Element;
