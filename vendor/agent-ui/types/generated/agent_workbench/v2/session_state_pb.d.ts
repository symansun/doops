import type { GenEnum, GenFile } from "@bufbuild/protobuf/codegenv2";
/**
 * Describes the file agent_workbench/v2/session_state.proto.
 */
export declare const file_agent_workbench_v2_session_state: GenFile;
/**
 * @generated from enum proto.agent_workbench.v2.MessageRole
 */
export declare enum MessageRole {
    /**
     * @generated from enum value: MESSAGE_ROLE_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: MESSAGE_ROLE_USER = 1;
     */
    USER = 1,
    /**
     * @generated from enum value: MESSAGE_ROLE_ASSISTANT = 2;
     */
    ASSISTANT = 2,
    /**
     * @generated from enum value: MESSAGE_ROLE_SYSTEM = 3;
     */
    SYSTEM = 3
}
/**
 * Describes the enum proto.agent_workbench.v2.MessageRole.
 */
export declare const MessageRoleSchema: GenEnum<MessageRole>;
/**
 * @generated from enum proto.agent_workbench.v2.TimelineItemStatus
 */
export declare enum TimelineItemStatus {
    /**
     * @generated from enum value: TIMELINE_ITEM_STATUS_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: TIMELINE_ITEM_STATUS_PENDING = 1;
     */
    PENDING = 1,
    /**
     * @generated from enum value: TIMELINE_ITEM_STATUS_STREAMING = 2;
     */
    STREAMING = 2,
    /**
     * @generated from enum value: TIMELINE_ITEM_STATUS_RUNNING = 3;
     */
    RUNNING = 3,
    /**
     * @generated from enum value: TIMELINE_ITEM_STATUS_COMPLETED = 4;
     */
    COMPLETED = 4,
    /**
     * @generated from enum value: TIMELINE_ITEM_STATUS_FAILED = 5;
     */
    FAILED = 5,
    /**
     * @generated from enum value: TIMELINE_ITEM_STATUS_CANCELLED = 6;
     */
    CANCELLED = 6
}
/**
 * Describes the enum proto.agent_workbench.v2.TimelineItemStatus.
 */
export declare const TimelineItemStatusSchema: GenEnum<TimelineItemStatus>;
/**
 * @generated from enum proto.agent_workbench.v2.PlanStepStatus
 */
export declare enum PlanStepStatus {
    /**
     * @generated from enum value: PLAN_STEP_STATUS_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: PLAN_STEP_STATUS_PENDING = 1;
     */
    PENDING = 1,
    /**
     * @generated from enum value: PLAN_STEP_STATUS_RUNNING = 2;
     */
    RUNNING = 2,
    /**
     * @generated from enum value: PLAN_STEP_STATUS_COMPLETED = 3;
     */
    COMPLETED = 3,
    /**
     * @generated from enum value: PLAN_STEP_STATUS_FAILED = 4;
     */
    FAILED = 4,
    /**
     * @generated from enum value: PLAN_STEP_STATUS_SKIPPED = 5;
     */
    SKIPPED = 5
}
/**
 * Describes the enum proto.agent_workbench.v2.PlanStepStatus.
 */
export declare const PlanStepStatusSchema: GenEnum<PlanStepStatus>;
/**
 * @generated from enum proto.agent_workbench.v2.PermissionRequestState
 */
export declare enum PermissionRequestState {
    /**
     * @generated from enum value: PERMISSION_REQUEST_STATE_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: PERMISSION_REQUEST_STATE_PENDING = 1;
     */
    PENDING = 1,
    /**
     * @generated from enum value: PERMISSION_REQUEST_STATE_RESOLVED = 2;
     */
    RESOLVED = 2,
    /**
     * @generated from enum value: PERMISSION_REQUEST_STATE_EXPIRED = 3;
     */
    EXPIRED = 3,
    /**
     * @generated from enum value: PERMISSION_REQUEST_STATE_CANCELLED = 4;
     */
    CANCELLED = 4
}
/**
 * Describes the enum proto.agent_workbench.v2.PermissionRequestState.
 */
export declare const PermissionRequestStateSchema: GenEnum<PermissionRequestState>;
/**
 * @generated from enum proto.agent_workbench.v2.SessionStatus
 */
export declare enum SessionStatus {
    /**
     * @generated from enum value: SESSION_STATUS_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: SESSION_STATUS_IDLE = 1;
     */
    IDLE = 1,
    /**
     * @generated from enum value: SESSION_STATUS_LAUNCHING = 2;
     */
    LAUNCHING = 2,
    /**
     * @generated from enum value: SESSION_STATUS_RUNNING = 3;
     */
    RUNNING = 3,
    /**
     * @generated from enum value: SESSION_STATUS_WAITING = 4;
     */
    WAITING = 4,
    /**
     * @generated from enum value: SESSION_STATUS_COMPLETED = 5;
     */
    COMPLETED = 5,
    /**
     * @generated from enum value: SESSION_STATUS_FAILED = 6;
     */
    FAILED = 6,
    /**
     * @generated from enum value: SESSION_STATUS_RESYNC_REQUIRED = 7;
     */
    RESYNC_REQUIRED = 7
}
/**
 * Describes the enum proto.agent_workbench.v2.SessionStatus.
 */
export declare const SessionStatusSchema: GenEnum<SessionStatus>;
/**
 * @generated from enum proto.agent_workbench.v2.SessionResourceStatus
 */
export declare enum SessionResourceStatus {
    /**
     * @generated from enum value: SESSION_RESOURCE_STATUS_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: SESSION_RESOURCE_STATUS_CONNECTING = 1;
     */
    CONNECTING = 1,
    /**
     * @generated from enum value: SESSION_RESOURCE_STATUS_READY = 2;
     */
    READY = 2,
    /**
     * @generated from enum value: SESSION_RESOURCE_STATUS_DEGRADED = 3;
     */
    DEGRADED = 3,
    /**
     * @generated from enum value: SESSION_RESOURCE_STATUS_OFFLINE = 4;
     */
    OFFLINE = 4,
    /**
     * @generated from enum value: SESSION_RESOURCE_STATUS_FAILED = 5;
     */
    FAILED = 5
}
/**
 * Describes the enum proto.agent_workbench.v2.SessionResourceStatus.
 */
export declare const SessionResourceStatusSchema: GenEnum<SessionResourceStatus>;
/**
 * @generated from enum proto.agent_workbench.v2.TerminalControlMode
 */
export declare enum TerminalControlMode {
    /**
     * @generated from enum value: TERMINAL_CONTROL_MODE_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: TERMINAL_CONTROL_MODE_SURFACE = 1;
     */
    SURFACE = 1,
    /**
     * @generated from enum value: TERMINAL_CONTROL_MODE_HOST = 2;
     */
    HOST = 2,
    /**
     * @generated from enum value: TERMINAL_CONTROL_MODE_SHARED = 3;
     */
    SHARED = 3
}
/**
 * Describes the enum proto.agent_workbench.v2.TerminalControlMode.
 */
export declare const TerminalControlModeSchema: GenEnum<TerminalControlMode>;
/**
 * @generated from enum proto.agent_workbench.v2.TerminalLeaseState
 */
export declare enum TerminalLeaseState {
    /**
     * @generated from enum value: TERMINAL_LEASE_STATE_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: TERMINAL_LEASE_STATE_ACTIVE = 1;
     */
    ACTIVE = 1,
    /**
     * @generated from enum value: TERMINAL_LEASE_STATE_EXPIRED = 2;
     */
    EXPIRED = 2,
    /**
     * @generated from enum value: TERMINAL_LEASE_STATE_RELEASED = 3;
     */
    RELEASED = 3
}
/**
 * Describes the enum proto.agent_workbench.v2.TerminalLeaseState.
 */
export declare const TerminalLeaseStateSchema: GenEnum<TerminalLeaseState>;
