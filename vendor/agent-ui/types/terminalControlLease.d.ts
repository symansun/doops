interface TerminalLeaseRenewalInput {
    expiresAt: number;
    leaseId: string;
    now?: () => number;
    onError?: (error: unknown) => void;
    renew: (leaseId: string) => Promise<void>;
}
export declare function startTerminalLeaseRenewal({ expiresAt, leaseId, now, onError, renew, }: TerminalLeaseRenewalInput): () => void;
export {};
