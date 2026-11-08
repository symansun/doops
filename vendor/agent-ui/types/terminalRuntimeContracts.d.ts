import type { AgentConnectionStatus, TerminalResource } from "./contracts.js";
export interface TerminalControlLease {
    leaseId: string;
    expiresAt: number;
}
export interface TerminalRuntime {
    connect(resource: TerminalResource): Promise<void>;
    disconnect(resourceId: string): void;
    subscribeOutput(resourceId: string, listener: (bytes: Uint8Array) => void): () => void;
    subscribeStatus(resourceId: string, listener: (status: AgentConnectionStatus) => void): () => void;
    write(resourceId: string, bytes: Uint8Array): Promise<void>;
    resize(resourceId: string, columns: number, rows: number): Promise<void>;
    acquireControl(resourceId: string, clientLabel: string): Promise<TerminalControlLease>;
    renewControl(resourceId: string, leaseId: string): Promise<void>;
    releaseControl(resourceId: string, leaseId: string): Promise<void>;
}
