import type { GenEnum, GenFile, GenMessage } from "@bufbuild/protobuf/codegenv2";
import type { ArtifactReference } from "./artifact_pb.js";
import type { ContentBlock, StructuredPayload } from "./content_pb.js";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file agent_workbench/v2/tool.proto.
 */
export declare const file_agent_workbench_v2_tool: GenFile;
/**
 * @generated from message proto.agent_workbench.v2.ToolIdentity
 */
export type ToolIdentity = Message<"proto.agent_workbench.v2.ToolIdentity"> & {
    /**
     * @generated from field: string namespace = 1;
     */
    namespace: string;
    /**
     * @generated from field: string semantic_key = 2;
     */
    semanticKey: string;
    /**
     * @generated from field: string schema_version = 3;
     */
    schemaVersion: string;
    /**
     * @generated from field: optional string source_tool_name = 4;
     */
    sourceToolName?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolIdentity.
 * Use `create(ToolIdentitySchema)` to create a new message.
 */
export declare const ToolIdentitySchema: GenMessage<ToolIdentity>;
/**
 * @generated from message proto.agent_workbench.v2.ToolProgress
 */
export type ToolProgress = Message<"proto.agent_workbench.v2.ToolProgress"> & {
    /**
     * @generated from field: optional string stage = 1;
     */
    stage?: string | undefined;
    /**
     * @generated from field: optional string message = 2;
     */
    message?: string | undefined;
    /**
     * @generated from field: optional uint64 current = 3;
     */
    current?: bigint | undefined;
    /**
     * @generated from field: optional uint64 total = 4;
     */
    total?: bigint | undefined;
    /**
     * @generated from field: optional double fraction = 5;
     */
    fraction?: number | undefined;
    /**
     * @generated from field: optional string unit = 6;
     */
    unit?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolProgress.
 * Use `create(ToolProgressSchema)` to create a new message.
 */
export declare const ToolProgressSchema: GenMessage<ToolProgress>;
/**
 * @generated from message proto.agent_workbench.v2.ToolResult
 */
export type ToolResult = Message<"proto.agent_workbench.v2.ToolResult"> & {
    /**
     * @generated from field: string result_id = 1;
     */
    resultId: string;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ContentBlock blocks = 2;
     */
    blocks: ContentBlock[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ArtifactReference artifacts = 3;
     */
    artifacts: ArtifactReference[];
    /**
     * @generated from field: bool primary = 4;
     */
    primary: boolean;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolResult.
 * Use `create(ToolResultSchema)` to create a new message.
 */
export declare const ToolResultSchema: GenMessage<ToolResult>;
/**
 * @generated from message proto.agent_workbench.v2.ToolAction
 */
export type ToolAction = Message<"proto.agent_workbench.v2.ToolAction"> & {
    /**
     * @generated from field: string action_id = 1;
     */
    actionId: string;
    /**
     * @generated from field: string action_type = 2;
     */
    actionType: string;
    /**
     * @generated from field: string schema_version = 3;
     */
    schemaVersion: string;
    /**
     * @generated from field: optional string label = 4;
     */
    label?: string | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload input_schema = 5;
     */
    inputSchema?: StructuredPayload | undefined;
    /**
     * @generated from field: optional string required_grant_action = 6;
     */
    requiredGrantAction?: string | undefined;
    /**
     * @generated from field: optional string risk_class = 7;
     */
    riskClass?: string | undefined;
    /**
     * @generated from field: bool confirmation_required = 8;
     */
    confirmationRequired: boolean;
    /**
     * @generated from field: optional string expires_at = 9;
     */
    expiresAt?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolAction.
 * Use `create(ToolActionSchema)` to create a new message.
 */
export declare const ToolActionSchema: GenMessage<ToolAction>;
/**
 * @generated from message proto.agent_workbench.v2.ToolTiming
 */
export type ToolTiming = Message<"proto.agent_workbench.v2.ToolTiming"> & {
    /**
     * @generated from field: optional string queued_at = 1;
     */
    queuedAt?: string | undefined;
    /**
     * @generated from field: optional string started_at = 2;
     */
    startedAt?: string | undefined;
    /**
     * @generated from field: optional string finished_at = 3;
     */
    finishedAt?: string | undefined;
    /**
     * @generated from field: optional uint64 duration_millis = 4;
     */
    durationMillis?: bigint | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolTiming.
 * Use `create(ToolTimingSchema)` to create a new message.
 */
export declare const ToolTimingSchema: GenMessage<ToolTiming>;
/**
 * @generated from message proto.agent_workbench.v2.ToolExecutorContext
 */
export type ToolExecutorContext = Message<"proto.agent_workbench.v2.ToolExecutorContext"> & {
    /**
     * @generated from field: optional string runner_id = 1;
     */
    runnerId?: string | undefined;
    /**
     * @generated from field: optional string pod_key = 2;
     */
    podKey?: string | undefined;
    /**
     * @generated from field: optional string process_id = 3;
     */
    processId?: string | undefined;
    /**
     * @generated from field: optional string workspace_id = 4;
     */
    workspaceId?: string | undefined;
    /**
     * @generated from field: optional string model_id = 5;
     */
    modelId?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolExecutorContext.
 * Use `create(ToolExecutorContextSchema)` to create a new message.
 */
export declare const ToolExecutorContextSchema: GenMessage<ToolExecutorContext>;
/**
 * @generated from message proto.agent_workbench.v2.ToolRetryLineage
 */
export type ToolRetryLineage = Message<"proto.agent_workbench.v2.ToolRetryLineage"> & {
    /**
     * @generated from field: optional string retry_of_execution_id = 1;
     */
    retryOfExecutionId?: string | undefined;
    /**
     * @generated from field: uint32 attempt = 2;
     */
    attempt: number;
    /**
     * @generated from field: optional uint32 maximum_attempts = 3;
     */
    maximumAttempts?: number | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolRetryLineage.
 * Use `create(ToolRetryLineageSchema)` to create a new message.
 */
export declare const ToolRetryLineageSchema: GenMessage<ToolRetryLineage>;
/**
 * @generated from message proto.agent_workbench.v2.ToolApprovalReference
 */
export type ToolApprovalReference = Message<"proto.agent_workbench.v2.ToolApprovalReference"> & {
    /**
     * @generated from field: string permission_request_id = 1;
     */
    permissionRequestId: string;
    /**
     * @generated from field: optional string approval_id = 2;
     */
    approvalId?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolApprovalReference.
 * Use `create(ToolApprovalReferenceSchema)` to create a new message.
 */
export declare const ToolApprovalReferenceSchema: GenMessage<ToolApprovalReference>;
/**
 * @generated from message proto.agent_workbench.v2.ToolFailure
 */
export type ToolFailure = Message<"proto.agent_workbench.v2.ToolFailure"> & {
    /**
     * @generated from field: string code = 1;
     */
    code: string;
    /**
     * @generated from field: string message = 2;
     */
    message: string;
    /**
     * @generated from field: bool retryable = 3;
     */
    retryable: boolean;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload details = 4;
     */
    details?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolFailure.
 * Use `create(ToolFailureSchema)` to create a new message.
 */
export declare const ToolFailureSchema: GenMessage<ToolFailure>;
/**
 * @generated from message proto.agent_workbench.v2.ToolExecution
 */
export type ToolExecution = Message<"proto.agent_workbench.v2.ToolExecution"> & {
    /**
     * @generated from field: string execution_id = 1;
     */
    executionId: string;
    /**
     * @generated from field: proto.agent_workbench.v2.ToolIdentity identity = 2;
     */
    identity?: ToolIdentity | undefined;
    /**
     * @generated from field: optional string category = 3;
     */
    category?: string | undefined;
    /**
     * @generated from field: proto.agent_workbench.v2.ToolPhase phase = 4;
     */
    phase: ToolPhase;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload input = 5;
     */
    input?: StructuredPayload | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ToolProgress progress = 6;
     */
    progress?: ToolProgress | undefined;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ToolResult results = 7;
     */
    results: ToolResult[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ArtifactReference artifacts = 8;
     */
    artifacts: ArtifactReference[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ToolAction actions = 9;
     */
    actions: ToolAction[];
    /**
     * @generated from field: optional proto.agent_workbench.v2.ToolTiming timing = 10;
     */
    timing?: ToolTiming | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ToolExecutorContext executor = 11;
     */
    executor?: ToolExecutorContext | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ToolRetryLineage retry = 12;
     */
    retry?: ToolRetryLineage | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ToolApprovalReference approval = 13;
     */
    approval?: ToolApprovalReference | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ToolFailure failure = 14;
     */
    failure?: ToolFailure | undefined;
    /**
     * @generated from field: optional string title = 15;
     */
    title?: string | undefined;
    /**
     * @generated from field: optional string detail = 16;
     */
    detail?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ToolExecution.
 * Use `create(ToolExecutionSchema)` to create a new message.
 */
export declare const ToolExecutionSchema: GenMessage<ToolExecution>;
/**
 * @generated from enum proto.agent_workbench.v2.ToolPhase
 */
export declare enum ToolPhase {
    /**
     * @generated from enum value: TOOL_PHASE_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: TOOL_PHASE_QUEUED = 1;
     */
    QUEUED = 1,
    /**
     * @generated from enum value: TOOL_PHASE_RUNNING = 2;
     */
    RUNNING = 2,
    /**
     * @generated from enum value: TOOL_PHASE_WAITING_APPROVAL = 3;
     */
    WAITING_APPROVAL = 3,
    /**
     * @generated from enum value: TOOL_PHASE_COMPLETED = 4;
     */
    COMPLETED = 4,
    /**
     * @generated from enum value: TOOL_PHASE_FAILED = 5;
     */
    FAILED = 5,
    /**
     * @generated from enum value: TOOL_PHASE_CANCELLED = 6;
     */
    CANCELLED = 6
}
/**
 * Describes the enum proto.agent_workbench.v2.ToolPhase.
 */
export declare const ToolPhaseSchema: GenEnum<ToolPhase>;
