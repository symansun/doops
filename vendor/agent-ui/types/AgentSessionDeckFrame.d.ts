import { type ReactNode } from "react";
import type { AgentSessionDeckEntry } from "./AgentSessionDeckTab.js";
import type { AgentWorkspaceLocale } from "./agentWorkspaceText.js";
export interface AgentSessionDeckFrameProps<TEntry extends AgentSessionDeckEntry = AgentSessionDeckEntry> {
    activeSessionId?: string;
    className?: string;
    defaultActiveSessionId?: string;
    locale?: AgentWorkspaceLocale;
    onActiveSessionChange?: (sessionId: string) => void;
    onCloseSession?: (sessionId: string) => void;
    onCreateSession?: () => void;
    sessionDeckMode?: "full" | "compact";
    renderSession: (entry: TEntry, active: boolean) => ReactNode;
    sessions: readonly TEntry[];
}
export declare function AgentSessionDeckFrame<TEntry extends AgentSessionDeckEntry>({ activeSessionId, className, defaultActiveSessionId, locale, onActiveSessionChange, onCloseSession, onCreateSession, sessionDeckMode, renderSession, sessions, }: AgentSessionDeckFrameProps<TEntry>): import("react/jsx-runtime").JSX.Element;
