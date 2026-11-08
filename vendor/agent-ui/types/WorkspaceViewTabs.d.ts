export type WorkspaceView = "conversation" | "terminal";
export declare function WorkspaceViewTabs({ conversationPanelId, conversationTabId, onViewChange, terminalEnabled, terminalPanelId, terminalTabId, view, }: {
    conversationPanelId: string;
    conversationTabId: string;
    onViewChange: (view: WorkspaceView) => void;
    terminalEnabled: boolean;
    terminalPanelId: string;
    terminalTabId: string;
    view: WorkspaceView;
}): import("react/jsx-runtime").JSX.Element;
