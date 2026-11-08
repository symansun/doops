import type { GenFile, GenMessage } from "@bufbuild/protobuf/codegenv2";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file events/v1/event_data.proto.
 */
export declare const file_events_v1_event_data: GenFile;
/**
 * @generated from message proto.events.v1.PodStatusChangedEventData
 */
export type PodStatusChangedEventData = Message<"proto.events.v1.PodStatusChangedEventData"> & {
    /**
     * @generated from field: string pod_key = 1;
     */
    podKey: string;
    /**
     * @generated from field: string status = 2;
     */
    status: string;
    /**
     * @generated from field: string previous_status = 3;
     */
    previousStatus: string;
    /**
     * @generated from field: string agent_status = 4;
     */
    agentStatus: string;
    /**
     * @generated from field: string error_code = 5;
     */
    errorCode: string;
    /**
     * @generated from field: string error_message = 6;
     */
    errorMessage: string;
};
/**
 * Describes the message proto.events.v1.PodStatusChangedEventData.
 * Use `create(PodStatusChangedEventDataSchema)` to create a new message.
 */
export declare const PodStatusChangedEventDataSchema: GenMessage<PodStatusChangedEventData>;
/**
 * @generated from message proto.events.v1.PodCreatedEventData
 */
export type PodCreatedEventData = Message<"proto.events.v1.PodCreatedEventData"> & {
    /**
     * @generated from field: string pod_key = 1;
     */
    podKey: string;
    /**
     * @generated from field: string status = 2;
     */
    status: string;
    /**
     * @generated from field: string agent_status = 3;
     */
    agentStatus: string;
    /**
     * @generated from field: int64 runner_id = 4;
     */
    runnerId: bigint;
    /**
     * @generated from field: optional int64 ticket_id = 5;
     */
    ticketId?: bigint | undefined;
    /**
     * @generated from field: string ticket_slug = 6;
     */
    ticketSlug: string;
    /**
     * @generated from field: int64 created_by_id = 7;
     */
    createdById: bigint;
};
/**
 * Describes the message proto.events.v1.PodCreatedEventData.
 * Use `create(PodCreatedEventDataSchema)` to create a new message.
 */
export declare const PodCreatedEventDataSchema: GenMessage<PodCreatedEventData>;
/**
 * @generated from message proto.events.v1.PodTitleChangedEventData
 */
export type PodTitleChangedEventData = Message<"proto.events.v1.PodTitleChangedEventData"> & {
    /**
     * @generated from field: string pod_key = 1;
     */
    podKey: string;
    /**
     * @generated from field: string title = 2;
     */
    title: string;
};
/**
 * Describes the message proto.events.v1.PodTitleChangedEventData.
 * Use `create(PodTitleChangedEventDataSchema)` to create a new message.
 */
export declare const PodTitleChangedEventDataSchema: GenMessage<PodTitleChangedEventData>;
/**
 * @generated from message proto.events.v1.PodAliasChangedEventData
 */
export type PodAliasChangedEventData = Message<"proto.events.v1.PodAliasChangedEventData"> & {
    /**
     * @generated from field: string pod_key = 1;
     */
    podKey: string;
    /**
     * @generated from field: optional string alias = 2;
     */
    alias?: string | undefined;
};
/**
 * Describes the message proto.events.v1.PodAliasChangedEventData.
 * Use `create(PodAliasChangedEventDataSchema)` to create a new message.
 */
export declare const PodAliasChangedEventDataSchema: GenMessage<PodAliasChangedEventData>;
/**
 * @generated from message proto.events.v1.PodInitProgressEventData
 */
export type PodInitProgressEventData = Message<"proto.events.v1.PodInitProgressEventData"> & {
    /**
     * @generated from field: string pod_key = 1;
     */
    podKey: string;
    /**
     * @generated from field: string phase = 2;
     */
    phase: string;
    /**
     * @generated from field: int32 progress = 3;
     */
    progress: number;
    /**
     * @generated from field: string message = 4;
     */
    message: string;
};
/**
 * Describes the message proto.events.v1.PodInitProgressEventData.
 * Use `create(PodInitProgressEventDataSchema)` to create a new message.
 */
export declare const PodInitProgressEventDataSchema: GenMessage<PodInitProgressEventData>;
/**
 * @generated from message proto.events.v1.PodRestartingEventData
 */
export type PodRestartingEventData = Message<"proto.events.v1.PodRestartingEventData"> & {
    /**
     * @generated from field: string pod_key = 1;
     */
    podKey: string;
    /**
     * @generated from field: int32 exit_code = 2;
     */
    exitCode: number;
    /**
     * @generated from field: int32 restart_count = 3;
     */
    restartCount: number;
};
/**
 * Describes the message proto.events.v1.PodRestartingEventData.
 * Use `create(PodRestartingEventDataSchema)` to create a new message.
 */
export declare const PodRestartingEventDataSchema: GenMessage<PodRestartingEventData>;
/**
 * @generated from message proto.events.v1.PodPerpetualChangedEventData
 */
export type PodPerpetualChangedEventData = Message<"proto.events.v1.PodPerpetualChangedEventData"> & {
    /**
     * @generated from field: string pod_key = 1;
     */
    podKey: string;
    /**
     * @generated from field: bool perpetual = 2;
     */
    perpetual: boolean;
};
/**
 * Describes the message proto.events.v1.PodPerpetualChangedEventData.
 * Use `create(PodPerpetualChangedEventDataSchema)` to create a new message.
 */
export declare const PodPerpetualChangedEventDataSchema: GenMessage<PodPerpetualChangedEventData>;
/**
 * @generated from message proto.events.v1.RunnerStatusEventData
 */
export type RunnerStatusEventData = Message<"proto.events.v1.RunnerStatusEventData"> & {
    /**
     * @generated from field: int64 runner_id = 1;
     */
    runnerId: bigint;
    /**
     * @generated from field: string node_id = 2;
     */
    nodeId: string;
    /**
     * @generated from field: string status = 3;
     */
    status: string;
    /**
     * @generated from field: int32 current_pods = 4;
     */
    currentPods: number;
    /**
     * @generated from field: string last_heartbeat = 5;
     */
    lastHeartbeat: string;
};
/**
 * Describes the message proto.events.v1.RunnerStatusEventData.
 * Use `create(RunnerStatusEventDataSchema)` to create a new message.
 */
export declare const RunnerStatusEventDataSchema: GenMessage<RunnerStatusEventData>;
/**
 * @generated from message proto.events.v1.TicketStatusChangedEventData
 */
export type TicketStatusChangedEventData = Message<"proto.events.v1.TicketStatusChangedEventData"> & {
    /**
     * @generated from field: string slug = 1;
     */
    slug: string;
    /**
     * @generated from field: string status = 2;
     */
    status: string;
    /**
     * @generated from field: string previous_status = 3;
     */
    previousStatus: string;
};
/**
 * Describes the message proto.events.v1.TicketStatusChangedEventData.
 * Use `create(TicketStatusChangedEventDataSchema)` to create a new message.
 */
export declare const TicketStatusChangedEventDataSchema: GenMessage<TicketStatusChangedEventData>;
/**
 * @generated from message proto.events.v1.SenderPodAgentEventData
 */
export type SenderPodAgentEventData = Message<"proto.events.v1.SenderPodAgentEventData"> & {
    /**
     * @generated from field: string name = 1;
     */
    name: string;
};
/**
 * Describes the message proto.events.v1.SenderPodAgentEventData.
 * Use `create(SenderPodAgentEventDataSchema)` to create a new message.
 */
export declare const SenderPodAgentEventDataSchema: GenMessage<SenderPodAgentEventData>;
/**
 * @generated from message proto.events.v1.SenderPodInfoEventData
 */
export type SenderPodInfoEventData = Message<"proto.events.v1.SenderPodInfoEventData"> & {
    /**
     * @generated from field: string pod_key = 1;
     */
    podKey: string;
    /**
     * @generated from field: optional string alias = 2;
     */
    alias?: string | undefined;
    /**
     * @generated from field: optional proto.events.v1.SenderPodAgentEventData agent = 3;
     */
    agent?: SenderPodAgentEventData | undefined;
};
/**
 * Describes the message proto.events.v1.SenderPodInfoEventData.
 * Use `create(SenderPodInfoEventDataSchema)` to create a new message.
 */
export declare const SenderPodInfoEventDataSchema: GenMessage<SenderPodInfoEventData>;
/**
 * @generated from message proto.events.v1.ChannelMessageEventData
 */
export type ChannelMessageEventData = Message<"proto.events.v1.ChannelMessageEventData"> & {
    /**
     * @generated from field: int64 id = 1;
     */
    id: bigint;
    /**
     * @generated from field: int64 channel_id = 2;
     */
    channelId: bigint;
    /**
     * @generated from field: optional string sender_pod = 3;
     */
    senderPod?: string | undefined;
    /**
     * @generated from field: optional int64 sender_user_id = 4;
     */
    senderUserId?: bigint | undefined;
    /**
     * @generated from field: string sender_name = 5;
     */
    senderName: string;
    /**
     * @generated from field: optional proto.events.v1.SenderPodInfoEventData sender_pod_info = 6;
     */
    senderPodInfo?: SenderPodInfoEventData | undefined;
    /**
     * @generated from field: string message_type = 7;
     */
    messageType: string;
    /**
     * @generated from field: string body = 8;
     */
    body: string;
    /**
     * Lexical/Slate AST — per-node variant schemas owned by renderer.
     *
     * @generated from field: string content_json = 9;
     */
    contentJson: string;
    /**
     * @generated from field: string mentions_json = 10;
     */
    mentionsJson: string;
    /**
     * @generated from field: optional int64 reply_to = 11;
     */
    replyTo?: bigint | undefined;
    /**
     * @generated from field: string created_at = 12;
     */
    createdAt: string;
};
/**
 * Describes the message proto.events.v1.ChannelMessageEventData.
 * Use `create(ChannelMessageEventDataSchema)` to create a new message.
 */
export declare const ChannelMessageEventDataSchema: GenMessage<ChannelMessageEventData>;
/**
 * @generated from message proto.events.v1.ChannelMessageEditedEventData
 */
export type ChannelMessageEditedEventData = Message<"proto.events.v1.ChannelMessageEditedEventData"> & {
    /**
     * @generated from field: int64 id = 1;
     */
    id: bigint;
    /**
     * @generated from field: int64 channel_id = 2;
     */
    channelId: bigint;
    /**
     * @generated from field: string body = 3;
     */
    body: string;
    /**
     * @generated from field: string content_json = 4;
     */
    contentJson: string;
    /**
     * @generated from field: string mentions_json = 5;
     */
    mentionsJson: string;
    /**
     * @generated from field: string edited_at = 6;
     */
    editedAt: string;
};
/**
 * Describes the message proto.events.v1.ChannelMessageEditedEventData.
 * Use `create(ChannelMessageEditedEventDataSchema)` to create a new message.
 */
export declare const ChannelMessageEditedEventDataSchema: GenMessage<ChannelMessageEditedEventData>;
/**
 * @generated from message proto.events.v1.ChannelMessageDeletedEventData
 */
export type ChannelMessageDeletedEventData = Message<"proto.events.v1.ChannelMessageDeletedEventData"> & {
    /**
     * @generated from field: int64 id = 1;
     */
    id: bigint;
    /**
     * @generated from field: int64 channel_id = 2;
     */
    channelId: bigint;
};
/**
 * Describes the message proto.events.v1.ChannelMessageDeletedEventData.
 * Use `create(ChannelMessageDeletedEventDataSchema)` to create a new message.
 */
export declare const ChannelMessageDeletedEventDataSchema: GenMessage<ChannelMessageDeletedEventData>;
/**
 * @generated from message proto.events.v1.ChannelMemberChangedEventData
 */
export type ChannelMemberChangedEventData = Message<"proto.events.v1.ChannelMemberChangedEventData"> & {
    /**
     * @generated from field: int64 channel_id = 1;
     */
    channelId: bigint;
    /**
     * @generated from field: int64 user_id = 2;
     */
    userId: bigint;
    /**
     * @generated from field: string role = 3;
     */
    role: string;
};
/**
 * Describes the message proto.events.v1.ChannelMemberChangedEventData.
 * Use `create(ChannelMemberChangedEventDataSchema)` to create a new message.
 */
export declare const ChannelMemberChangedEventDataSchema: GenMessage<ChannelMemberChangedEventData>;
/**
 * @generated from message proto.events.v1.AutopilotStatusChangedEventData
 */
export type AutopilotStatusChangedEventData = Message<"proto.events.v1.AutopilotStatusChangedEventData"> & {
    /**
     * @generated from field: string autopilot_controller_key = 1;
     */
    autopilotControllerKey: string;
    /**
     * @generated from field: string pod_key = 2;
     */
    podKey: string;
    /**
     * @generated from field: string phase = 3;
     */
    phase: string;
    /**
     * @generated from field: int32 current_iteration = 4;
     */
    currentIteration: number;
    /**
     * @generated from field: int32 max_iterations = 5;
     */
    maxIterations: number;
    /**
     * @generated from field: string circuit_breaker_state = 6;
     */
    circuitBreakerState: string;
    /**
     * @generated from field: string circuit_breaker_reason = 7;
     */
    circuitBreakerReason: string;
    /**
     * @generated from field: bool user_takeover = 8;
     */
    userTakeover: boolean;
};
/**
 * Describes the message proto.events.v1.AutopilotStatusChangedEventData.
 * Use `create(AutopilotStatusChangedEventDataSchema)` to create a new message.
 */
export declare const AutopilotStatusChangedEventDataSchema: GenMessage<AutopilotStatusChangedEventData>;
/**
 * @generated from message proto.events.v1.AutopilotIterationEventData
 */
export type AutopilotIterationEventData = Message<"proto.events.v1.AutopilotIterationEventData"> & {
    /**
     * @generated from field: string autopilot_controller_key = 1;
     */
    autopilotControllerKey: string;
    /**
     * @generated from field: int32 iteration = 2;
     */
    iteration: number;
    /**
     * @generated from field: string phase = 3;
     */
    phase: string;
    /**
     * @generated from field: string summary = 4;
     */
    summary: string;
    /**
     * @generated from field: repeated string files_changed = 5;
     */
    filesChanged: string[];
    /**
     * @generated from field: int64 duration_ms = 6;
     */
    durationMs: bigint;
};
/**
 * Describes the message proto.events.v1.AutopilotIterationEventData.
 * Use `create(AutopilotIterationEventDataSchema)` to create a new message.
 */
export declare const AutopilotIterationEventDataSchema: GenMessage<AutopilotIterationEventData>;
/**
 * @generated from message proto.events.v1.AutopilotCreatedEventData
 */
export type AutopilotCreatedEventData = Message<"proto.events.v1.AutopilotCreatedEventData"> & {
    /**
     * @generated from field: string autopilot_controller_key = 1;
     */
    autopilotControllerKey: string;
    /**
     * @generated from field: string pod_key = 2;
     */
    podKey: string;
};
/**
 * Describes the message proto.events.v1.AutopilotCreatedEventData.
 * Use `create(AutopilotCreatedEventDataSchema)` to create a new message.
 */
export declare const AutopilotCreatedEventDataSchema: GenMessage<AutopilotCreatedEventData>;
/**
 * @generated from message proto.events.v1.AutopilotTerminatedEventData
 */
export type AutopilotTerminatedEventData = Message<"proto.events.v1.AutopilotTerminatedEventData"> & {
    /**
     * @generated from field: string autopilot_controller_key = 1;
     */
    autopilotControllerKey: string;
    /**
     * @generated from field: string phase = 2;
     */
    phase: string;
    /**
     * @generated from field: string reason = 3;
     */
    reason: string;
};
/**
 * Describes the message proto.events.v1.AutopilotTerminatedEventData.
 * Use `create(AutopilotTerminatedEventDataSchema)` to create a new message.
 */
export declare const AutopilotTerminatedEventDataSchema: GenMessage<AutopilotTerminatedEventData>;
/**
 * @generated from message proto.events.v1.AutopilotActionEventData
 */
export type AutopilotActionEventData = Message<"proto.events.v1.AutopilotActionEventData"> & {
    /**
     * @generated from field: string type = 1;
     */
    type: string;
    /**
     * @generated from field: string content = 2;
     */
    content: string;
    /**
     * @generated from field: string reason = 3;
     */
    reason: string;
};
/**
 * Describes the message proto.events.v1.AutopilotActionEventData.
 * Use `create(AutopilotActionEventDataSchema)` to create a new message.
 */
export declare const AutopilotActionEventDataSchema: GenMessage<AutopilotActionEventData>;
/**
 * @generated from message proto.events.v1.AutopilotProgressEventData
 */
export type AutopilotProgressEventData = Message<"proto.events.v1.AutopilotProgressEventData"> & {
    /**
     * @generated from field: string summary = 1;
     */
    summary: string;
    /**
     * @generated from field: repeated string completed_steps = 2;
     */
    completedSteps: string[];
    /**
     * @generated from field: repeated string remaining_steps = 3;
     */
    remainingSteps: string[];
    /**
     * @generated from field: int32 percent = 4;
     */
    percent: number;
};
/**
 * Describes the message proto.events.v1.AutopilotProgressEventData.
 * Use `create(AutopilotProgressEventDataSchema)` to create a new message.
 */
export declare const AutopilotProgressEventDataSchema: GenMessage<AutopilotProgressEventData>;
/**
 * @generated from message proto.events.v1.AutopilotHelpSuggestionEventData
 */
export type AutopilotHelpSuggestionEventData = Message<"proto.events.v1.AutopilotHelpSuggestionEventData"> & {
    /**
     * @generated from field: string action = 1;
     */
    action: string;
    /**
     * @generated from field: string label = 2;
     */
    label: string;
};
/**
 * Describes the message proto.events.v1.AutopilotHelpSuggestionEventData.
 * Use `create(AutopilotHelpSuggestionEventDataSchema)` to create a new message.
 */
export declare const AutopilotHelpSuggestionEventDataSchema: GenMessage<AutopilotHelpSuggestionEventData>;
/**
 * @generated from message proto.events.v1.AutopilotHelpRequestEventData
 */
export type AutopilotHelpRequestEventData = Message<"proto.events.v1.AutopilotHelpRequestEventData"> & {
    /**
     * @generated from field: string reason = 1;
     */
    reason: string;
    /**
     * @generated from field: string context = 2;
     */
    context: string;
    /**
     * @generated from field: string terminal_excerpt = 3;
     */
    terminalExcerpt: string;
    /**
     * @generated from field: repeated proto.events.v1.AutopilotHelpSuggestionEventData suggestions = 4;
     */
    suggestions: AutopilotHelpSuggestionEventData[];
};
/**
 * Describes the message proto.events.v1.AutopilotHelpRequestEventData.
 * Use `create(AutopilotHelpRequestEventDataSchema)` to create a new message.
 */
export declare const AutopilotHelpRequestEventDataSchema: GenMessage<AutopilotHelpRequestEventData>;
/**
 * @generated from message proto.events.v1.AutopilotThinkingEventData
 */
export type AutopilotThinkingEventData = Message<"proto.events.v1.AutopilotThinkingEventData"> & {
    /**
     * @generated from field: string autopilot_controller_key = 1;
     */
    autopilotControllerKey: string;
    /**
     * @generated from field: int32 iteration = 2;
     */
    iteration: number;
    /**
     * @generated from field: string decision_type = 3;
     */
    decisionType: string;
    /**
     * @generated from field: string reasoning = 4;
     */
    reasoning: string;
    /**
     * @generated from field: double confidence = 5;
     */
    confidence: number;
    /**
     * @generated from field: optional proto.events.v1.AutopilotActionEventData action = 6;
     */
    action?: AutopilotActionEventData | undefined;
    /**
     * @generated from field: optional proto.events.v1.AutopilotProgressEventData progress = 7;
     */
    progress?: AutopilotProgressEventData | undefined;
    /**
     * @generated from field: optional proto.events.v1.AutopilotHelpRequestEventData help_request = 8;
     */
    helpRequest?: AutopilotHelpRequestEventData | undefined;
};
/**
 * Describes the message proto.events.v1.AutopilotThinkingEventData.
 * Use `create(AutopilotThinkingEventDataSchema)` to create a new message.
 */
export declare const AutopilotThinkingEventDataSchema: GenMessage<AutopilotThinkingEventData>;
/**
 * @generated from message proto.events.v1.MrEventData
 */
export type MrEventData = Message<"proto.events.v1.MrEventData"> & {
    /**
     * @generated from field: int64 mr_id = 1;
     */
    mrId: bigint;
    /**
     * @generated from field: int32 mr_iid = 2;
     */
    mrIid: number;
    /**
     * @generated from field: string mr_url = 3;
     */
    mrUrl: string;
    /**
     * @generated from field: string source_branch = 4;
     */
    sourceBranch: string;
    /**
     * @generated from field: string target_branch = 5;
     */
    targetBranch: string;
    /**
     * @generated from field: string title = 6;
     */
    title: string;
    /**
     * @generated from field: string state = 7;
     */
    state: string;
    /**
     * @generated from field: string action = 8;
     */
    action: string;
    /**
     * @generated from field: optional int64 ticket_id = 9;
     */
    ticketId?: bigint | undefined;
    /**
     * @generated from field: string ticket_slug = 10;
     */
    ticketSlug: string;
    /**
     * @generated from field: optional int64 pod_id = 11;
     */
    podId?: bigint | undefined;
    /**
     * @generated from field: int64 repository_id = 12;
     */
    repositoryId: bigint;
    /**
     * @generated from field: string pipeline_status = 13;
     */
    pipelineStatus: string;
};
/**
 * Describes the message proto.events.v1.MrEventData.
 * Use `create(MrEventDataSchema)` to create a new message.
 */
export declare const MrEventDataSchema: GenMessage<MrEventData>;
/**
 * @generated from message proto.events.v1.PipelineEventData
 */
export type PipelineEventData = Message<"proto.events.v1.PipelineEventData"> & {
    /**
     * @generated from field: int64 mr_id = 1;
     */
    mrId: bigint;
    /**
     * @generated from field: int64 pipeline_id = 2;
     */
    pipelineId: bigint;
    /**
     * @generated from field: string pipeline_status = 3;
     */
    pipelineStatus: string;
    /**
     * @generated from field: string pipeline_url = 4;
     */
    pipelineUrl: string;
    /**
     * @generated from field: string source_branch = 5;
     */
    sourceBranch: string;
    /**
     * @generated from field: optional int64 ticket_id = 6;
     */
    ticketId?: bigint | undefined;
    /**
     * @generated from field: string ticket_slug = 7;
     */
    ticketSlug: string;
    /**
     * @generated from field: optional int64 pod_id = 8;
     */
    podId?: bigint | undefined;
    /**
     * @generated from field: int64 repository_id = 9;
     */
    repositoryId: bigint;
};
/**
 * Describes the message proto.events.v1.PipelineEventData.
 * Use `create(PipelineEventDataSchema)` to create a new message.
 */
export declare const PipelineEventDataSchema: GenMessage<PipelineEventData>;
/**
 * @generated from message proto.events.v1.WorkflowRunWarningEventData
 */
export type WorkflowRunWarningEventData = Message<"proto.events.v1.WorkflowRunWarningEventData"> & {
    /**
     * @generated from field: int64 workflow_id = 1;
     */
    workflowId: bigint;
    /**
     * @generated from field: int64 run_id = 2;
     */
    runId: bigint;
    /**
     * @generated from field: int32 run_number = 3;
     */
    runNumber: number;
    /**
     * @generated from field: string warning = 4;
     */
    warning: string;
    /**
     * @generated from field: string detail = 5;
     */
    detail: string;
};
/**
 * Describes the message proto.events.v1.WorkflowRunWarningEventData.
 * Use `create(WorkflowRunWarningEventDataSchema)` to create a new message.
 */
export declare const WorkflowRunWarningEventDataSchema: GenMessage<WorkflowRunWarningEventData>;
/**
 * @generated from message proto.events.v1.WorkflowRunEventData
 */
export type WorkflowRunEventData = Message<"proto.events.v1.WorkflowRunEventData"> & {
    /**
     * @generated from field: int64 workflow_id = 1;
     */
    workflowId: bigint;
    /**
     * @generated from field: int64 run_id = 2;
     */
    runId: bigint;
    /**
     * @generated from field: int32 run_number = 3;
     */
    runNumber: number;
    /**
     * @generated from field: string status = 4;
     */
    status: string;
    /**
     * @generated from field: optional string pod_key = 5;
     */
    podKey?: string | undefined;
};
/**
 * Describes the message proto.events.v1.WorkflowRunEventData.
 * Use `create(WorkflowRunEventDataSchema)` to create a new message.
 */
export declare const WorkflowRunEventDataSchema: GenMessage<WorkflowRunEventData>;
/**
 * @generated from message proto.events.v1.NotificationPayloadEventData
 */
export type NotificationPayloadEventData = Message<"proto.events.v1.NotificationPayloadEventData"> & {
    /**
     * @generated from field: string source = 1;
     */
    source: string;
    /**
     * @generated from field: string title = 2;
     */
    title: string;
    /**
     * @generated from field: string body = 3;
     */
    body: string;
    /**
     * @generated from field: string link = 4;
     */
    link: string;
    /**
     * @generated from field: string priority = 5;
     */
    priority: string;
    /**
     * @generated from field: map<string, bool> channels = 6;
     */
    channels: {
        [key: string]: boolean;
    };
};
/**
 * Describes the message proto.events.v1.NotificationPayloadEventData.
 * Use `create(NotificationPayloadEventDataSchema)` to create a new message.
 */
export declare const NotificationPayloadEventDataSchema: GenMessage<NotificationPayloadEventData>;
