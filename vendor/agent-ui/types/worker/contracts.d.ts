import type { AgentSessionRuntime } from "../agentSessionRuntime.js";
import type { CodeWorkspaceSource } from "../workbench/codeWorkspaceSource.js";
import type { WorkerLiveness } from "./liveness/workerLiveness.js";
import type { AgentHarnessDescriptor } from "./agentHarnessDescriptor.js";
import type { AgentAdapter } from "./agentAdapter.js";
export type { CodeWorkspaceSource };
export interface WorkerAuth {
    baseUrl: string;
    getAccessToken(): Promise<string>;
    orgSlug?: string;
}
export interface WorkerRef {
    transport: "pod";
    podKey: string;
    sessionId?: string;
}
export type WorkerRecovery = {
    kind: "cli";
    command: string;
} | {
    kind: "resume-directory";
    sourceHostId: string | null;
} | {
    kind: "fork";
} | {
    kind: "wait";
} | {
    kind: "retry";
};
export interface WorkspaceFileEntry {
    name: string;
    path: string;
    type: "file" | "directory";
}
export interface WorkspaceFileSource {
    list(sessionId: string, dir: string): Promise<WorkspaceFileEntry[]>;
    exists?(sessionId: string, path: string): Promise<boolean>;
}
export interface WorkerDirectorySource {
}
export interface WorkerTransport {
    readonly kind: WorkerRef["transport"];
    resolveSession(ref: WorkerRef, signal?: AbortSignal): Promise<string>;
    runtimeFor(sessionId: string): AgentSessionRuntime;
    harnessForSession?(sessionId: string): AgentHarnessDescriptor;
    adapterForSession?(sessionId: string): AgentAdapter | undefined;
    closeSession?(sessionId: string): void;
    subscribeLiveness(ref: WorkerRef, listener: (liveness: WorkerLiveness) => void): () => void;
    workspaceFiles?: WorkspaceFileSource;
    codeWorkspace?: CodeWorkspaceSource;
    directory?: WorkerDirectorySource;
}
export declare function refKey(ref: WorkerRef): string;
