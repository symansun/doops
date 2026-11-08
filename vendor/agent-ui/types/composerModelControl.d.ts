import type { ReactNode } from "react";
import type { AgentSessionSnapshot } from "./contracts.js";
export type ComposerModelControlRenderer = (state: {
    snapshot: AgentSessionSnapshot;
    disabled: boolean;
}) => ReactNode;
