import type { AgentInteractionEvent, AgentInteractionStatus } from "../agentInteractionContracts.js";
export declare class AgentInteractionStore {
    private readonly events;
    clear(): void;
    get(id: string): AgentInteractionEvent | undefined;
    set(event: AgentInteractionEvent): void;
    updateStatus(commandId: string, status: AgentInteractionStatus, updatedAt: string): AgentInteractionEvent | undefined;
    values(): AgentInteractionEvent[];
    merge(persisted: readonly AgentInteractionEvent[]): AgentInteractionEvent[];
}
export declare function isTerminalInteraction(status: string): boolean;
