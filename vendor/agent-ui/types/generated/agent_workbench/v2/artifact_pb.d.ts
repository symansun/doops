import type { GenEnum, GenFile, GenMessage } from "@bufbuild/protobuf/codegenv2";
import type { StructuredPayload, UnsupportedValue } from "./content_pb.js";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file agent_workbench/v2/artifact.proto.
 */
export declare const file_agent_workbench_v2_artifact: GenFile;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactReference
 */
export type ArtifactReference = Message<"proto.agent_workbench.v2.ArtifactReference"> & {
    /**
     * @generated from field: string artifact_id = 1;
     */
    artifactId: string;
    /**
     * @generated from field: optional string representation_id = 2;
     */
    representationId?: string | undefined;
    /**
     * @generated from field: optional uint64 revision = 3;
     */
    revision?: bigint | undefined;
    /**
     * @generated from field: optional string role = 4;
     */
    role?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactReference.
 * Use `create(ArtifactReferenceSchema)` to create a new message.
 */
export declare const ArtifactReferenceSchema: GenMessage<ArtifactReference>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactDimensions
 */
export type ArtifactDimensions = Message<"proto.agent_workbench.v2.ArtifactDimensions"> & {
    /**
     * @generated from field: uint32 width = 1;
     */
    width: number;
    /**
     * @generated from field: uint32 height = 2;
     */
    height: number;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactDimensions.
 * Use `create(ArtifactDimensionsSchema)` to create a new message.
 */
export declare const ArtifactDimensionsSchema: GenMessage<ArtifactDimensions>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactTransport
 */
export type ArtifactTransport = Message<"proto.agent_workbench.v2.ArtifactTransport"> & {
    /**
     * @generated from oneof proto.agent_workbench.v2.ArtifactTransport.transport
     */
    transport: {
        /**
         * @generated from field: string download_url = 1;
         */
        value: string;
        case: "downloadUrl";
    } | {
        /**
         * @generated from field: string resource_id = 2;
         */
        value: string;
        case: "resourceId";
    } | {
        /**
         * @generated from field: string inline_text = 3;
         */
        value: string;
        case: "inlineText";
    } | {
        /**
         * @generated from field: bytes inline_bytes = 4;
         */
        value: Uint8Array;
        case: "inlineBytes";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactTransport.
 * Use `create(ArtifactTransportSchema)` to create a new message.
 */
export declare const ArtifactTransportSchema: GenMessage<ArtifactTransport>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactRepresentation
 */
export type ArtifactRepresentation = Message<"proto.agent_workbench.v2.ArtifactRepresentation"> & {
    /**
     * @generated from field: string representation_id = 1;
     */
    representationId: string;
    /**
     * @generated from field: uint64 revision = 2;
     */
    revision: bigint;
    /**
     * @generated from field: string media_type = 3;
     */
    mediaType: string;
    /**
     * @generated from field: optional string role = 4;
     */
    role?: string | undefined;
    /**
     * @generated from field: optional string filename = 5;
     */
    filename?: string | undefined;
    /**
     * @generated from field: proto.agent_workbench.v2.ArtifactStatus status = 6;
     */
    status: ArtifactStatus;
    /**
     * @generated from field: optional uint64 byte_size = 7;
     */
    byteSize?: bigint | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ArtifactDimensions dimensions = 8;
     */
    dimensions?: ArtifactDimensions | undefined;
    /**
     * @generated from field: optional uint64 duration_millis = 9;
     */
    durationMillis?: bigint | undefined;
    /**
     * @generated from field: optional string digest = 10;
     */
    digest?: string | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ArtifactTransport transport = 11;
     */
    transport?: ArtifactTransport | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactRepresentation.
 * Use `create(ArtifactRepresentationSchema)` to create a new message.
 */
export declare const ArtifactRepresentationSchema: GenMessage<ArtifactRepresentation>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactProvenance
 */
export type ArtifactProvenance = Message<"proto.agent_workbench.v2.ArtifactProvenance"> & {
    /**
     * @generated from field: optional string producer_namespace = 1;
     */
    producerNamespace?: string | undefined;
    /**
     * @generated from field: optional string producer_type = 2;
     */
    producerType?: string | undefined;
    /**
     * @generated from field: optional string producer_id = 3;
     */
    producerId?: string | undefined;
    /**
     * @generated from field: optional string command_id = 4;
     */
    commandId?: string | undefined;
    /**
     * @generated from field: optional string tool_execution_id = 5;
     */
    toolExecutionId?: string | undefined;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ArtifactReference parents = 6;
     */
    parents: ArtifactReference[];
    /**
     * @generated from field: optional string created_at = 7;
     */
    createdAt?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactProvenance.
 * Use `create(ArtifactProvenanceSchema)` to create a new message.
 */
export declare const ArtifactProvenanceSchema: GenMessage<ArtifactProvenance>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactRevision
 */
export type ArtifactRevision = Message<"proto.agent_workbench.v2.ArtifactRevision"> & {
    /**
     * @generated from field: uint64 revision = 1;
     */
    revision: bigint;
    /**
     * @generated from field: optional uint64 base_revision = 2;
     */
    baseRevision?: bigint | undefined;
    /**
     * @generated from field: repeated string representation_ids = 3;
     */
    representationIds: string[];
    /**
     * @generated from field: optional string digest = 4;
     */
    digest?: string | undefined;
    /**
     * @generated from field: optional string created_at = 5;
     */
    createdAt?: string | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ArtifactProvenance provenance = 6;
     */
    provenance?: ArtifactProvenance | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactRevision.
 * Use `create(ArtifactRevisionSchema)` to create a new message.
 */
export declare const ArtifactRevisionSchema: GenMessage<ArtifactRevision>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactGrant
 */
export type ArtifactGrant = Message<"proto.agent_workbench.v2.ArtifactGrant"> & {
    /**
     * @generated from field: string grant_id = 1;
     */
    grantId: string;
    /**
     * @generated from field: optional string issuer = 2;
     */
    issuer?: string | undefined;
    /**
     * @generated from field: optional string subject = 3;
     */
    subject?: string | undefined;
    /**
     * @generated from field: repeated string representation_ids = 4;
     */
    representationIds: string[];
    /**
     * @generated from field: repeated string actions = 5;
     */
    actions: string[];
    /**
     * @generated from field: optional uint64 minimum_revision = 6;
     */
    minimumRevision?: bigint | undefined;
    /**
     * @generated from field: optional uint64 maximum_revision = 7;
     */
    maximumRevision?: bigint | undefined;
    /**
     * @generated from field: optional string issued_at = 8;
     */
    issuedAt?: string | undefined;
    /**
     * @generated from field: optional string expires_at = 9;
     */
    expiresAt?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactGrant.
 * Use `create(ArtifactGrantSchema)` to create a new message.
 */
export declare const ArtifactGrantSchema: GenMessage<ArtifactGrant>;
/**
 * @generated from message proto.agent_workbench.v2.NormalizedPoint
 */
export type NormalizedPoint = Message<"proto.agent_workbench.v2.NormalizedPoint"> & {
    /**
     * @generated from field: double x = 1;
     */
    x: number;
    /**
     * @generated from field: double y = 2;
     */
    y: number;
};
/**
 * Describes the message proto.agent_workbench.v2.NormalizedPoint.
 * Use `create(NormalizedPointSchema)` to create a new message.
 */
export declare const NormalizedPointSchema: GenMessage<NormalizedPoint>;
/**
 * @generated from message proto.agent_workbench.v2.NormalizedRegion
 */
export type NormalizedRegion = Message<"proto.agent_workbench.v2.NormalizedRegion"> & {
    /**
     * @generated from field: double x = 1;
     */
    x: number;
    /**
     * @generated from field: double y = 2;
     */
    y: number;
    /**
     * @generated from field: double width = 3;
     */
    width: number;
    /**
     * @generated from field: double height = 4;
     */
    height: number;
};
/**
 * Describes the message proto.agent_workbench.v2.NormalizedRegion.
 * Use `create(NormalizedRegionSchema)` to create a new message.
 */
export declare const NormalizedRegionSchema: GenMessage<NormalizedRegion>;
/**
 * @generated from message proto.agent_workbench.v2.ImageAnnotation
 */
export type ImageAnnotation = Message<"proto.agent_workbench.v2.ImageAnnotation"> & {
    /**
     * @generated from field: string annotation_id = 1;
     */
    annotationId: string;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.NormalizedPoint path = 2;
     */
    path: NormalizedPoint[];
    /**
     * @generated from field: optional string label = 3;
     */
    label?: string | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload style = 4;
     */
    style?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ImageAnnotation.
 * Use `create(ImageAnnotationSchema)` to create a new message.
 */
export declare const ImageAnnotationSchema: GenMessage<ImageAnnotation>;
/**
 * @generated from message proto.agent_workbench.v2.ImageEditManifest
 */
export type ImageEditManifest = Message<"proto.agent_workbench.v2.ImageEditManifest"> & {
    /**
     * @generated from field: string source_representation_id = 1;
     */
    sourceRepresentationId: string;
    /**
     * @generated from field: optional string result_representation_id = 2;
     */
    resultRepresentationId?: string | undefined;
    /**
     * @generated from field: repeated string candidate_representation_ids = 3;
     */
    candidateRepresentationIds: string[];
    /**
     * @generated from field: optional string mask_representation_id = 4;
     */
    maskRepresentationId?: string | undefined;
    /**
     * @generated from field: uint32 source_width = 5;
     */
    sourceWidth: number;
    /**
     * @generated from field: uint32 source_height = 6;
     */
    sourceHeight: number;
    /**
     * @generated from field: optional string exif_orientation = 7;
     */
    exifOrientation?: string | undefined;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.NormalizedRegion regions = 8;
     */
    regions: NormalizedRegion[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ImageAnnotation annotations = 9;
     */
    annotations: ImageAnnotation[];
};
/**
 * Describes the message proto.agent_workbench.v2.ImageEditManifest.
 * Use `create(ImageEditManifestSchema)` to create a new message.
 */
export declare const ImageEditManifestSchema: GenMessage<ImageEditManifest>;
/**
 * @generated from message proto.agent_workbench.v2.VideoManifest
 */
export type VideoManifest = Message<"proto.agent_workbench.v2.VideoManifest"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.VideoStage stage = 1;
     */
    stage: VideoStage;
    /**
     * @generated from field: optional double progress_fraction = 2;
     */
    progressFraction?: number | undefined;
    /**
     * @generated from field: optional uint64 duration_millis = 3;
     */
    durationMillis?: bigint | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ArtifactDimensions dimensions = 4;
     */
    dimensions?: ArtifactDimensions | undefined;
    /**
     * @generated from field: optional string original_representation_id = 5;
     */
    originalRepresentationId?: string | undefined;
    /**
     * @generated from field: optional string playable_representation_id = 6;
     */
    playableRepresentationId?: string | undefined;
    /**
     * @generated from field: optional string poster_representation_id = 7;
     */
    posterRepresentationId?: string | undefined;
    /**
     * @generated from field: repeated string thumbnail_representation_ids = 8;
     */
    thumbnailRepresentationIds: string[];
    /**
     * @generated from field: repeated string derivative_representation_ids = 9;
     */
    derivativeRepresentationIds: string[];
};
/**
 * Describes the message proto.agent_workbench.v2.VideoManifest.
 * Use `create(VideoManifestSchema)` to create a new message.
 */
export declare const VideoManifestSchema: GenMessage<VideoManifest>;
/**
 * @generated from message proto.agent_workbench.v2.PresentationSlide
 */
export type PresentationSlide = Message<"proto.agent_workbench.v2.PresentationSlide"> & {
    /**
     * @generated from field: string slide_id = 1;
     */
    slideId: string;
    /**
     * @generated from field: uint32 position = 2;
     */
    position: number;
    /**
     * @generated from field: optional string title = 3;
     */
    title?: string | undefined;
    /**
     * @generated from field: optional string notes = 4;
     */
    notes?: string | undefined;
    /**
     * @generated from field: optional string page_representation_id = 5;
     */
    pageRepresentationId?: string | undefined;
    /**
     * @generated from field: optional string thumbnail_representation_id = 6;
     */
    thumbnailRepresentationId?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PresentationSlide.
 * Use `create(PresentationSlideSchema)` to create a new message.
 */
export declare const PresentationSlideSchema: GenMessage<PresentationSlide>;
/**
 * @generated from message proto.agent_workbench.v2.PresentationVersion
 */
export type PresentationVersion = Message<"proto.agent_workbench.v2.PresentationVersion"> & {
    /**
     * @generated from field: string version_id = 1;
     */
    versionId: string;
    /**
     * @generated from field: uint64 revision = 2;
     */
    revision: bigint;
    /**
     * @generated from field: optional string label = 3;
     */
    label?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PresentationVersion.
 * Use `create(PresentationVersionSchema)` to create a new message.
 */
export declare const PresentationVersionSchema: GenMessage<PresentationVersion>;
/**
 * @generated from message proto.agent_workbench.v2.PresentationManifest
 */
export type PresentationManifest = Message<"proto.agent_workbench.v2.PresentationManifest"> & {
    /**
     * @generated from field: uint64 deck_revision = 1;
     */
    deckRevision: bigint;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.PresentationSlide slides = 2;
     */
    slides: PresentationSlide[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.PresentationVersion versions = 3;
     */
    versions: PresentationVersion[];
    /**
     * @generated from field: optional string selected_version_id = 4;
     */
    selectedVersionId?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.PresentationManifest.
 * Use `create(PresentationManifestSchema)` to create a new message.
 */
export declare const PresentationManifestSchema: GenMessage<PresentationManifest>;
/**
 * @generated from message proto.agent_workbench.v2.ExtensionArtifactManifest
 */
export type ExtensionArtifactManifest = Message<"proto.agent_workbench.v2.ExtensionArtifactManifest"> & {
    /**
     * @generated from field: string namespace = 1;
     */
    namespace: string;
    /**
     * @generated from field: string semantic_type = 2;
     */
    semanticType: string;
    /**
     * @generated from field: string schema_version = 3;
     */
    schemaVersion: string;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload payload = 4;
     */
    payload?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ExtensionArtifactManifest.
 * Use `create(ExtensionArtifactManifestSchema)` to create a new message.
 */
export declare const ExtensionArtifactManifestSchema: GenMessage<ExtensionArtifactManifest>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactManifest
 */
export type ArtifactManifest = Message<"proto.agent_workbench.v2.ArtifactManifest"> & {
    /**
     * @generated from oneof proto.agent_workbench.v2.ArtifactManifest.manifest
     */
    manifest: {
        /**
         * @generated from field: proto.agent_workbench.v2.ImageEditManifest image_edit = 1;
         */
        value: ImageEditManifest;
        case: "imageEdit";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.VideoManifest video = 2;
         */
        value: VideoManifest;
        case: "video";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.PresentationManifest presentation = 3;
         */
        value: PresentationManifest;
        case: "presentation";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ExtensionArtifactManifest extension = 4;
         */
        value: ExtensionArtifactManifest;
        case: "extension";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.UnsupportedValue unsupported = 100;
         */
        value: UnsupportedValue;
        case: "unsupported";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactManifest.
 * Use `create(ArtifactManifestSchema)` to create a new message.
 */
export declare const ArtifactManifestSchema: GenMessage<ArtifactManifest>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactDescriptor
 */
export type ArtifactDescriptor = Message<"proto.agent_workbench.v2.ArtifactDescriptor"> & {
    /**
     * @generated from field: string artifact_id = 1;
     */
    artifactId: string;
    /**
     * @generated from field: uint64 revision = 2;
     */
    revision: bigint;
    /**
     * @generated from field: string filename = 3;
     */
    filename: string;
    /**
     * @generated from field: string media_type = 4;
     */
    mediaType: string;
    /**
     * @generated from field: optional string role = 5;
     */
    role?: string | undefined;
    /**
     * @generated from field: proto.agent_workbench.v2.ArtifactStatus status = 6;
     */
    status: ArtifactStatus;
    /**
     * @generated from field: optional uint64 byte_size = 7;
     */
    byteSize?: bigint | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ArtifactDimensions dimensions = 8;
     */
    dimensions?: ArtifactDimensions | undefined;
    /**
     * @generated from field: optional uint64 duration_millis = 9;
     */
    durationMillis?: bigint | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.ArtifactProvenance provenance = 10;
     */
    provenance?: ArtifactProvenance | undefined;
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ArtifactRepresentation representations = 11;
     */
    representations: ArtifactRepresentation[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ArtifactRevision revisions = 12;
     */
    revisions: ArtifactRevision[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.ArtifactGrant grants = 13;
     */
    grants: ArtifactGrant[];
    /**
     * @generated from field: optional proto.agent_workbench.v2.ArtifactManifest manifest = 14;
     */
    manifest?: ArtifactManifest | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ArtifactDescriptor.
 * Use `create(ArtifactDescriptorSchema)` to create a new message.
 */
export declare const ArtifactDescriptorSchema: GenMessage<ArtifactDescriptor>;
/**
 * @generated from enum proto.agent_workbench.v2.ArtifactStatus
 */
export declare enum ArtifactStatus {
    /**
     * @generated from enum value: ARTIFACT_STATUS_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: ARTIFACT_STATUS_QUEUED = 1;
     */
    QUEUED = 1,
    /**
     * @generated from enum value: ARTIFACT_STATUS_PROCESSING = 2;
     */
    PROCESSING = 2,
    /**
     * @generated from enum value: ARTIFACT_STATUS_READY = 3;
     */
    READY = 3,
    /**
     * @generated from enum value: ARTIFACT_STATUS_FAILED = 4;
     */
    FAILED = 4,
    /**
     * @generated from enum value: ARTIFACT_STATUS_DELETED = 5;
     */
    DELETED = 5
}
/**
 * Describes the enum proto.agent_workbench.v2.ArtifactStatus.
 */
export declare const ArtifactStatusSchema: GenEnum<ArtifactStatus>;
/**
 * @generated from enum proto.agent_workbench.v2.VideoStage
 */
export declare enum VideoStage {
    /**
     * @generated from enum value: VIDEO_STAGE_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: VIDEO_STAGE_QUEUED = 1;
     */
    QUEUED = 1,
    /**
     * @generated from enum value: VIDEO_STAGE_RENDERING = 2;
     */
    RENDERING = 2,
    /**
     * @generated from enum value: VIDEO_STAGE_TRANSCODING = 3;
     */
    TRANSCODING = 3,
    /**
     * @generated from enum value: VIDEO_STAGE_READY = 4;
     */
    READY = 4,
    /**
     * @generated from enum value: VIDEO_STAGE_FAILED = 5;
     */
    FAILED = 5
}
/**
 * Describes the enum proto.agent_workbench.v2.VideoStage.
 */
export declare const VideoStageSchema: GenEnum<VideoStage>;
