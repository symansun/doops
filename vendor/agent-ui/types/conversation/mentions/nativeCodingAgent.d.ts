export type NativeCodingAgentKey = "claude" | "codex" | "cursor" | "pi" | string;
export interface NativeCodingAgentRef {
    key: NativeCodingAgentKey;
}
export declare function nativeCodingAgentForHarness(harness: string | null | undefined): NativeCodingAgentRef | undefined;
