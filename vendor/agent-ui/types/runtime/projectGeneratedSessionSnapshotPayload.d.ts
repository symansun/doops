import type { AgentError } from "../generated/agent_workbench/v2/command_pb.js";
import { type StructuredPayload, type UnsupportedValue } from "../generated/agent_workbench/v2/content_pb.js";
export interface DecodedStructuredPayload {
    text: string;
    value: unknown;
}
export declare function decodeStructuredPayload(payload: StructuredPayload | undefined): DecodedStructuredPayload | undefined;
export declare function formatStructuredPayload(payload: StructuredPayload | undefined): string | undefined;
export declare function formatUnsupported(value: UnsupportedValue): string;
export declare function formatAgentError(error: AgentError | undefined): string | null;
export declare function formatAgentErrorDetail(error: AgentError): string;
export declare function stringifyValue(value: unknown): string;
