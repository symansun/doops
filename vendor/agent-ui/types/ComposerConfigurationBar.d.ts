import { type ReactNode } from "react";
import type { AgentSessionRuntime, AgentSessionSnapshot } from "./contracts.js";
export declare function ComposerConfigurationBar({ modelControl, onError, runtime, snapshot, }: {
    modelControl?: ReactNode;
    onError: (error: unknown) => void;
    runtime: AgentSessionRuntime;
    snapshot: AgentSessionSnapshot;
}): import("react/jsx-runtime").JSX.Element;
