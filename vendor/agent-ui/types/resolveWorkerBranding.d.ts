export type AmpTenantBranding = {
    tenantName?: string | null;
    brandName?: string | null;
    brandSubtitle?: string | null;
    logoUrl?: string | null;
};
export type WorkerBrandingSpec = {
    accentColor?: string;
    showLogo: boolean;
    useTenantLogo: boolean;
    logoUrl?: string;
    title?: string;
    subtitle?: string;
};
export type ResolvedWorkerBranding = {
    accentColor?: string;
    showLogo: true;
    logoUrl: string;
    title: string;
    subtitle: string;
};
export declare function resolveWorkerBranding(spec: WorkerBrandingSpec | undefined, amp: AmpTenantBranding | null | undefined): ResolvedWorkerBranding | null;
