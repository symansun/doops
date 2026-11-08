import { type LucideIcon } from "lucide-react";
import type { AgentToolActivityItem } from "./agentToolContracts.js";
import type { AgentToolRendererRegistration } from "./react/rendererTypes.js";
export interface ResolvedToolActivityPresentation {
    icon: LucideIcon;
    inputLabel: string;
    label: string;
    outputLabel: string;
    specialized: boolean;
}
export declare function resolveToolActivityPresentation(item: AgentToolActivityItem, renderer?: AgentToolRendererRegistration): ResolvedToolActivityPresentation;
