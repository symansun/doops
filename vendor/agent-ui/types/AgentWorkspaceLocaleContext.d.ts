import { type AgentWorkspaceLocale } from "./agentWorkspaceText.js";
export declare function AgentWorkspaceLocaleProvider({ children, locale, }: {
    children: React.ReactNode;
    locale: AgentWorkspaceLocale;
}): import("react/jsx-runtime").JSX.Element;
export declare function useAgentWorkspaceText(): import("./agentWorkspaceText.js").AgentWorkspaceText;
export declare function useAgentWorkspaceLocale(): AgentWorkspaceLocale;
