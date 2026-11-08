import { type MessageInitShape } from "@bufbuild/protobuf";
import { CommandEnvelopeSchema, type CommandEnvelope } from "../generated/agent_workbench/v2/command_pb.js";
export interface CreateAgentCommandEnvelopeInput {
    command: MessageInitShape<typeof CommandEnvelopeSchema>["command"];
    commandId: string;
    expectedRevision?: bigint;
    issuedAt: string;
    sessionId: string;
    streamEpoch: string;
}
export declare function createAgentCommandEnvelope(input: CreateAgentCommandEnvelopeInput): Promise<CommandEnvelope>;
