import { type ArtifactActionCommand } from "./artifactAction.js";
import type { ArtifactDescriptor, ArtifactPayload, ArtifactRuntime } from "./ArtifactRuntime.js";
export declare class ArtifactController<TRepresentationPayload = ArtifactPayload, TActionResult = unknown> {
    private readonly runtime;
    private readonly actionResults;
    private readonly descriptors;
    private readonly representations;
    constructor(runtime: ArtifactRuntime<TRepresentationPayload, TActionResult>);
    updateDescriptor(descriptor: ArtifactDescriptor): void;
    getDescriptor(artifactId: string, representationId: string): ArtifactDescriptor | undefined;
    loadRepresentation(artifactId: string, representationId: string, revision: bigint): Promise<TRepresentationPayload>;
    execute(command: ArtifactActionCommand): Promise<TActionResult>;
}
