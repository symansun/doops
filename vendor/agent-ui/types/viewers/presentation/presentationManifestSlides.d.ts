import type { AgentArtifactRepresentation, AgentPresentationManifest, AgentPresentationSlide } from "../../contracts.js";
export interface RenderablePresentationSlide extends AgentPresentationSlide {
    pageRepresentationId: string;
}
export declare function presentationManifestSlides(manifest: AgentPresentationManifest, representations: readonly AgentArtifactRepresentation[]): RenderablePresentationSlide[];
export declare function presentationRepresentationIds(slides: readonly RenderablePresentationSlide[]): string[];
