import { type ReactNode } from "react";
import type { AgentArtifactItem, AgentSessionRuntime, AgentSessionSnapshot } from "./contracts.js";
export declare function MarkdownArtifactProvider({ children, runtime, snapshot, }: {
    children: ReactNode;
    runtime: AgentSessionRuntime;
    snapshot: AgentSessionSnapshot;
}): import("react/jsx-runtime").JSX.Element;
export declare function useMarkdownArtifact(source?: string): {
    artifact: AgentArtifactItem;
    runtime: AgentSessionRuntime;
    sessionId: string;
} | undefined;
