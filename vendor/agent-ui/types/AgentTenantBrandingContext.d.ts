import { type ReactNode } from "react";
import type { AmpTenantBranding } from "./resolveWorkerBranding.js";
export declare function AgentTenantBrandingProvider({ children, value, }: {
    children: ReactNode;
    value: AmpTenantBranding | null;
}): import("react/jsx-runtime").JSX.Element;
export declare function useAgentTenantBranding(): AmpTenantBranding | null;
