import { type AgentWorkspaceProps } from "./AgentWorkspace.js";
import type { AgentSessionDeckEntry } from "./AgentSessionDeckTab.js";
export interface AgentSessionDeckProps extends Omit<AgentWorkspaceProps, "runtime" | "sessionId" | "showFullscreen" | "terminalRuntime"> {
    activeSessionId?: string;
    defaultActiveSessionId?: string;
    onActiveSessionChange?: (sessionId: string) => void;
    onCloseSession?: (sessionId: string) => void;
    onCreateSession?: () => void;
    sessionDeckMode?: "full" | "compact";
    sessions: readonly AgentSessionDeckEntry[];
}
export type { AgentSessionDeckEntry };
export declare function AgentSessionDeck({ activeSessionId, className, defaultActiveSessionId, locale, onActiveSessionChange, onCloseSession, onCreateSession, sessionDeckMode, sessions, ...workspaceProps }: AgentSessionDeckProps): import("react/jsx-runtime").JSX.Element;
