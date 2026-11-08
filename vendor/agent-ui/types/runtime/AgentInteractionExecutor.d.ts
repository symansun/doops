import type { AgentInteractionKind } from "../agentInteractionContracts.js";
import { AgentInteractionStore } from "./AgentInteractionStore.js";
export declare class AgentInteractionExecutor {
    private readonly store;
    private readonly now;
    private readonly refresh;
    constructor(store: AgentInteractionStore, now: () => string, refresh: () => void);
    execute<T>(kind: AgentInteractionKind, commandId: string, sourceId: string | undefined, operation: () => Promise<T>): Promise<T>;
}
