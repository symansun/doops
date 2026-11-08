import type { AgentArtifactItem, AgentSessionRuntime } from "./contracts.js";
export declare function GenericArtifactCard({ filename, fill, item, presentation, runtime, sessionId, }: {
    filename: string;
    fill?: boolean;
    item: AgentArtifactItem;
    presentation?: "developer" | "user";
    runtime: AgentSessionRuntime;
    sessionId: string;
}): import("react/jsx-runtime").JSX.Element;
export declare function ArtifactError({ filename, message, }: {
    filename: string;
    message: string;
}): import("react/jsx-runtime").JSX.Element;
