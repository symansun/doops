export declare function isRetryableAgentSessionError(cause: unknown): boolean;
export declare function agentSessionRetryDelayMs(attempt: number, random?: () => number): number;
export declare function defaultAgentSessionRetryDelay(attempt: number, signal: AbortSignal): Promise<void>;
