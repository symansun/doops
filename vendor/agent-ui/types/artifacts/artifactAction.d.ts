export interface ArtifactActionCommand<TActionType extends string = string, TPayload = unknown> {
    readonly artifactId: string;
    readonly representationId: string;
    readonly baseRevision: bigint;
    readonly clientActionId: string;
    readonly actionType: TActionType;
    readonly payload: TPayload;
}
export declare function artifactAction<const TActionType extends string, TPayload>(command: ArtifactActionCommand<TActionType, TPayload>): ArtifactActionCommand<TActionType, TPayload>;
export declare class ArtifactRevisionConflictError extends Error {
    readonly code = "artifact_revision_conflict";
    readonly baseRevision: bigint;
    readonly currentRevision: bigint;
    constructor(baseRevision: bigint, currentRevision: bigint);
}
export declare class ArtifactActionIdConflictError extends Error {
    readonly code = "artifact_action_id_conflict";
    readonly clientActionId: string;
    constructor(clientActionId: string);
}
