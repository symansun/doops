import type { AgentSessionRuntime, TerminalRuntime } from "./contracts.js";
export interface AgentSessionDeckEntry {
    fallbackTitle?: string;
    runtime?: AgentSessionRuntime;
    sessionId: string;
    terminalRuntime?: TerminalRuntime;
}
export declare function AgentSessionDeckTab({ active, entry, id, onActivate, onClose, panelId, }: {
    active: boolean;
    entry: AgentSessionDeckEntry;
    id: string;
    onActivate: () => void;
    onClose?: () => void;
    panelId: string;
}): import("react/jsx-runtime").JSX.Element;
