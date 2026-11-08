import type { AgentSessionSnapshot } from "./contracts.js";
import type { AgentSessionRuntime } from "./contracts.js";
export declare function ComposerCapabilityBar({ modelControl, onError, runtime, snapshot, }: {
    modelControl?: React.ReactNode;
    onError: (error: unknown) => void;
    runtime: AgentSessionRuntime;
    snapshot: AgentSessionSnapshot;
}): import("react/jsx-runtime").JSX.Element;
