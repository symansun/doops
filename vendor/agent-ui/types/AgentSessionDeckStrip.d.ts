import { type AgentSessionDeckEntry } from "./AgentSessionDeckTab.js";
export declare function AgentSessionDeckStrip({ activeSessionId, baseId, controlsRef, fullscreenActive, fullscreenSupported, onActivate, onCloseSession, onCreateSession, onToggleFullscreen, compact, showFullscreen, sessions, }: {
    activeSessionId?: string;
    baseId: string;
    controlsRef: (node: HTMLDivElement | null) => void;
    fullscreenActive: boolean;
    fullscreenSupported: boolean;
    onActivate: (sessionId: string) => void;
    onCloseSession?: (sessionId: string) => void;
    onCreateSession?: () => void;
    onToggleFullscreen: () => void;
    compact?: boolean;
    showFullscreen?: boolean;
    sessions: readonly AgentSessionDeckEntry[];
}): import("react/jsx-runtime").JSX.Element;
export declare function AgentSessionDeckTabs({ activeSessionId, baseId, onActivate, onCloseSession, sessions, }: Pick<AgentSessionDeckStripProps, "activeSessionId" | "baseId" | "onActivate" | "onCloseSession" | "sessions">): import("react/jsx-runtime").JSX.Element;
export declare function AgentSessionDeckActions({ fullscreenActive, fullscreenSupported, onCreateSession, onToggleFullscreen, }: Pick<AgentSessionDeckStripProps, "fullscreenActive" | "fullscreenSupported" | "onCreateSession" | "onToggleFullscreen">): import("react/jsx-runtime").JSX.Element;
interface AgentSessionDeckStripProps {
    activeSessionId?: string;
    baseId: string;
    fullscreenActive: boolean;
    fullscreenSupported: boolean;
    onActivate: (sessionId: string) => void;
    onCloseSession?: (sessionId: string) => void;
    onCreateSession?: () => void;
    onToggleFullscreen: () => void;
    sessions: readonly AgentSessionDeckEntry[];
}
export {};
