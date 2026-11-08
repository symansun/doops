import type { AgentPartnerIdentity, AgentSessionRuntime, AgentSessionSnapshot } from "../contracts.js";
import type { AgentSessionConnection } from "./AgentSessionConnection.js";
import type { AgentSessionProjectionContext } from "./agentSessionRuntimeProjection.js";
import type { SessionNotificationScheduler } from "./sessionFrameNotifier.js";
export interface AgentArtifactLoadRequest {
    artifactId: string;
    representationId?: string;
    sessionId: string;
}
export interface AgentSessionRuntimeV2Options extends AgentSessionProjectionContext {
    connection: AgentSessionConnection;
    loadArtifact?: (request: AgentArtifactLoadRequest) => Promise<Blob>;
    loadAttachment?: AgentSessionRuntime["loadAttachment"];
    loadPreviewUrl?: AgentSessionRuntime["loadPreviewUrl"];
    uploadAttachment?: AgentSessionRuntime["uploadAttachment"];
    projectSnapshot?: (snapshot: AgentSessionSnapshot) => AgentSessionSnapshot;
    now?: () => string;
    scheduleNotification?: SessionNotificationScheduler;
    partner?: AgentPartnerIdentity;
}
