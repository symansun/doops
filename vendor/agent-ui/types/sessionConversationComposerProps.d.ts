import type { ComposerModelControlRenderer } from "./composerModelControl.js";
import type { WorkspaceFileSource } from "./conversation/mentions/workspaceFileSource.js";
import type { AgentWorkspacePresentation } from "./userWorkspacePresentation.js";
import type { SessionRecovery } from "./sessionRecoveryActions.js";
import type { WorkspaceCodeReference } from "./workbench/workspaceCodeReference.js";
import type { AgentSessionRuntime, AgentSessionSnapshot } from "./contracts.js";
export interface SessionConversationComposerProps {
    onError: (error: unknown) => void;
    onSendingChange?: (sessionId: string, sending: boolean) => void;
    presentation: AgentWorkspacePresentation;
    mobile?: boolean;
    runtime: AgentSessionRuntime;
    renderModelControl?: ComposerModelControlRenderer;
    snapshot: AgentSessionSnapshot;
    workspaceFiles?: WorkspaceFileSource;
    mentionHarness?: string | null;
    sessionRecovery?: SessionRecovery;
    codeReferences?: readonly WorkspaceCodeReference[];
    onCodeReferencesSent?: () => void;
    onRemoveCodeReference?: (index: number) => void;
}
