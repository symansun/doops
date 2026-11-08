import type { GenFile, GenMessage, GenService } from "@bufbuild/protobuf/codegenv2";
import type { CommandEnvelope, CommandReceiptSchema } from "./command_pb.js";
import type { SessionCursor, SessionDeltaBatchSchema, SessionSnapshotSchema } from "./session_pb.js";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file agent_workbench/v2/service.proto.
 */
export declare const file_agent_workbench_v2_service: GenFile;
/**
 * @generated from message proto.agent_workbench.v2.GetSessionSnapshotRequest
 */
export type GetSessionSnapshotRequest = Message<"proto.agent_workbench.v2.GetSessionSnapshotRequest"> & {
    /**
     * @generated from field: string tenant_id = 1;
     */
    tenantId: string;
    /**
     * @generated from field: string session_id = 2;
     */
    sessionId: string;
    /**
     * Zero preserves full-history responses for existing clients.
     *
     * @generated from field: uint32 history_limit = 3;
     */
    historyLimit: number;
};
/**
 * Describes the message proto.agent_workbench.v2.GetSessionSnapshotRequest.
 * Use `create(GetSessionSnapshotRequestSchema)` to create a new message.
 */
export declare const GetSessionSnapshotRequestSchema: GenMessage<GetSessionSnapshotRequest>;
/**
 * @generated from message proto.agent_workbench.v2.StreamSessionDeltasRequest
 */
export type StreamSessionDeltasRequest = Message<"proto.agent_workbench.v2.StreamSessionDeltasRequest"> & {
    /**
     * @generated from field: string tenant_id = 1;
     */
    tenantId: string;
    /**
     * @generated from field: proto.agent_workbench.v2.SessionCursor cursor = 2;
     */
    cursor?: SessionCursor | undefined;
    /**
     * @generated from field: uint32 replay_limit = 3;
     */
    replayLimit: number;
};
/**
 * Describes the message proto.agent_workbench.v2.StreamSessionDeltasRequest.
 * Use `create(StreamSessionDeltasRequestSchema)` to create a new message.
 */
export declare const StreamSessionDeltasRequestSchema: GenMessage<StreamSessionDeltasRequest>;
/**
 * @generated from message proto.agent_workbench.v2.ExecuteCommandRequest
 */
export type ExecuteCommandRequest = Message<"proto.agent_workbench.v2.ExecuteCommandRequest"> & {
    /**
     * @generated from field: string tenant_id = 1;
     */
    tenantId: string;
    /**
     * @generated from field: proto.agent_workbench.v2.CommandEnvelope command = 2;
     */
    command?: CommandEnvelope | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ExecuteCommandRequest.
 * Use `create(ExecuteCommandRequestSchema)` to create a new message.
 */
export declare const ExecuteCommandRequestSchema: GenMessage<ExecuteCommandRequest>;
/**
 * @generated from service proto.agent_workbench.v2.AgentWorkbenchService
 */
export declare const AgentWorkbenchService: GenService<{
    /**
     * @generated from rpc proto.agent_workbench.v2.AgentWorkbenchService.GetSessionSnapshot
     */
    getSessionSnapshot: {
        methodKind: "unary";
        input: typeof GetSessionSnapshotRequestSchema;
        output: typeof SessionSnapshotSchema;
    };
    /**
     * @generated from rpc proto.agent_workbench.v2.AgentWorkbenchService.StreamSessionDeltas
     */
    streamSessionDeltas: {
        methodKind: "server_streaming";
        input: typeof StreamSessionDeltasRequestSchema;
        output: typeof SessionDeltaBatchSchema;
    };
    /**
     * @generated from rpc proto.agent_workbench.v2.AgentWorkbenchService.ExecuteCommand
     */
    executeCommand: {
        methodKind: "unary";
        input: typeof ExecuteCommandRequestSchema;
        output: typeof CommandReceiptSchema;
    };
}>;
