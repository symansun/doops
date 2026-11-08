import type { PresentationActionPayload, PresentationArtifactAction, PresentationGrant } from "./presentationContracts.js";
export interface PresentationActionContext {
    readonly actionSchemaVersion: string;
    readonly artifactId: string;
    readonly baseRevision: bigint;
    readonly representationId?: string;
}
export declare function createPresentationAction(context: PresentationActionContext, actionType: PresentationGrant, payload: PresentationActionPayload): PresentationArtifactAction;
