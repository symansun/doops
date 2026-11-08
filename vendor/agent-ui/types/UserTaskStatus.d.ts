import type { AgentArtifactItem, AgentSessionSnapshot } from "./contracts.js";
export declare function UserTaskStatus({ artifacts, snapshot, }: {
    artifacts: readonly AgentArtifactItem[];
    snapshot: AgentSessionSnapshot;
}): import("react/jsx-runtime").JSX.Element | null;
