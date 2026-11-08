import type { GenEnum, GenFile, GenMessage } from "@bufbuild/protobuf/codegenv2";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file agent_workbench/v2/content.proto.
 */
export declare const file_agent_workbench_v2_content: GenFile;
/**
 * @generated from message proto.agent_workbench.v2.ContentIdentity
 */
export type ContentIdentity = Message<"proto.agent_workbench.v2.ContentIdentity"> & {
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
     * @generated from field: optional string source_type = 4;
     */
    sourceType?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ContentIdentity.
 * Use `create(ContentIdentitySchema)` to create a new message.
 */
export declare const ContentIdentitySchema: GenMessage<ContentIdentity>;
/**
 * @generated from message proto.agent_workbench.v2.StructuredPayload
 */
export type StructuredPayload = Message<"proto.agent_workbench.v2.StructuredPayload"> & {
    /**
     * @generated from field: string media_type = 1;
     */
    mediaType: string;
    /**
     * @generated from field: bytes data = 2;
     */
    data: Uint8Array;
};
/**
 * Describes the message proto.agent_workbench.v2.StructuredPayload.
 * Use `create(StructuredPayloadSchema)` to create a new message.
 */
export declare const StructuredPayloadSchema: GenMessage<StructuredPayload>;
/**
 * @generated from message proto.agent_workbench.v2.UnsupportedValue
 */
export type UnsupportedValue = Message<"proto.agent_workbench.v2.UnsupportedValue"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.ContentIdentity identity = 1;
     */
    identity?: ContentIdentity | undefined;
    /**
     * @generated from field: proto.agent_workbench.v2.UnsupportedReason reason = 2;
     */
    reason: UnsupportedReason;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload payload = 3;
     */
    payload?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.UnsupportedValue.
 * Use `create(UnsupportedValueSchema)` to create a new message.
 */
export declare const UnsupportedValueSchema: GenMessage<UnsupportedValue>;
/**
 * @generated from message proto.agent_workbench.v2.TextContent
 */
export type TextContent = Message<"proto.agent_workbench.v2.TextContent"> & {
    /**
     * @generated from field: string text = 1;
     */
    text: string;
};
/**
 * Describes the message proto.agent_workbench.v2.TextContent.
 * Use `create(TextContentSchema)` to create a new message.
 */
export declare const TextContentSchema: GenMessage<TextContent>;
/**
 * @generated from message proto.agent_workbench.v2.MarkdownContent
 */
export type MarkdownContent = Message<"proto.agent_workbench.v2.MarkdownContent"> & {
    /**
     * @generated from field: string markdown = 1;
     */
    markdown: string;
};
/**
 * Describes the message proto.agent_workbench.v2.MarkdownContent.
 * Use `create(MarkdownContentSchema)` to create a new message.
 */
export declare const MarkdownContentSchema: GenMessage<MarkdownContent>;
/**
 * @generated from message proto.agent_workbench.v2.CodeContent
 */
export type CodeContent = Message<"proto.agent_workbench.v2.CodeContent"> & {
    /**
     * @generated from field: string code = 1;
     */
    code: string;
    /**
     * @generated from field: optional string language = 2;
     */
    language?: string | undefined;
    /**
     * @generated from field: optional string filename = 3;
     */
    filename?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.CodeContent.
 * Use `create(CodeContentSchema)` to create a new message.
 */
export declare const CodeContentSchema: GenMessage<CodeContent>;
/**
 * @generated from message proto.agent_workbench.v2.JsonContent
 */
export type JsonContent = Message<"proto.agent_workbench.v2.JsonContent"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload value = 1;
     */
    value?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.JsonContent.
 * Use `create(JsonContentSchema)` to create a new message.
 */
export declare const JsonContentSchema: GenMessage<JsonContent>;
/**
 * @generated from message proto.agent_workbench.v2.TableColumn
 */
export type TableColumn = Message<"proto.agent_workbench.v2.TableColumn"> & {
    /**
     * @generated from field: string key = 1;
     */
    key: string;
    /**
     * @generated from field: string label = 2;
     */
    label: string;
    /**
     * @generated from field: optional string value_type = 3;
     */
    valueType?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.TableColumn.
 * Use `create(TableColumnSchema)` to create a new message.
 */
export declare const TableColumnSchema: GenMessage<TableColumn>;
/**
 * @generated from message proto.agent_workbench.v2.TableRow
 */
export type TableRow = Message<"proto.agent_workbench.v2.TableRow"> & {
    /**
     * @generated from field: repeated proto.agent_workbench.v2.StructuredPayload cells = 1;
     */
    cells: StructuredPayload[];
};
/**
 * Describes the message proto.agent_workbench.v2.TableRow.
 * Use `create(TableRowSchema)` to create a new message.
 */
export declare const TableRowSchema: GenMessage<TableRow>;
/**
 * @generated from message proto.agent_workbench.v2.TableContent
 */
export type TableContent = Message<"proto.agent_workbench.v2.TableContent"> & {
    /**
     * @generated from field: repeated proto.agent_workbench.v2.TableColumn columns = 1;
     */
    columns: TableColumn[];
    /**
     * @generated from field: repeated proto.agent_workbench.v2.TableRow rows = 2;
     */
    rows: TableRow[];
};
/**
 * Describes the message proto.agent_workbench.v2.TableContent.
 * Use `create(TableContentSchema)` to create a new message.
 */
export declare const TableContentSchema: GenMessage<TableContent>;
/**
 * @generated from message proto.agent_workbench.v2.DiffContent
 */
export type DiffContent = Message<"proto.agent_workbench.v2.DiffContent"> & {
    /**
     * @generated from field: string patch = 1;
     */
    patch: string;
    /**
     * @generated from field: optional string path = 2;
     */
    path?: string | undefined;
    /**
     * @generated from field: optional string language = 3;
     */
    language?: string | undefined;
    /**
     * @generated from field: bool truncated = 4;
     */
    truncated: boolean;
};
/**
 * Describes the message proto.agent_workbench.v2.DiffContent.
 * Use `create(DiffContentSchema)` to create a new message.
 */
export declare const DiffContentSchema: GenMessage<DiffContent>;
/**
 * @generated from message proto.agent_workbench.v2.CommandContent
 */
export type CommandContent = Message<"proto.agent_workbench.v2.CommandContent"> & {
    /**
     * @generated from field: string command = 1;
     */
    command: string;
    /**
     * @generated from field: optional string cwd = 2;
     */
    cwd?: string | undefined;
    /**
     * @generated from field: optional int32 exit_code = 3;
     */
    exitCode?: number | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.CommandContent.
 * Use `create(CommandContentSchema)` to create a new message.
 */
export declare const CommandContentSchema: GenMessage<CommandContent>;
/**
 * @generated from message proto.agent_workbench.v2.LogContent
 */
export type LogContent = Message<"proto.agent_workbench.v2.LogContent"> & {
    /**
     * @generated from field: string level = 1;
     */
    level: string;
    /**
     * @generated from field: string message = 2;
     */
    message: string;
    /**
     * @generated from field: optional string created_at = 3;
     */
    createdAt?: string | undefined;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload fields = 4;
     */
    fields?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.LogContent.
 * Use `create(LogContentSchema)` to create a new message.
 */
export declare const LogContentSchema: GenMessage<LogContent>;
/**
 * @generated from message proto.agent_workbench.v2.ProgressContent
 */
export type ProgressContent = Message<"proto.agent_workbench.v2.ProgressContent"> & {
    /**
     * @generated from field: string stage = 1;
     */
    stage: string;
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
 * Describes the message proto.agent_workbench.v2.ProgressContent.
 * Use `create(ProgressContentSchema)` to create a new message.
 */
export declare const ProgressContentSchema: GenMessage<ProgressContent>;
/**
 * @generated from message proto.agent_workbench.v2.ErrorContent
 */
export type ErrorContent = Message<"proto.agent_workbench.v2.ErrorContent"> & {
    /**
     * @generated from field: string code = 1;
     */
    code: string;
    /**
     * @generated from field: string message = 2;
     */
    message: string;
    /**
     * @generated from field: optional proto.agent_workbench.v2.StructuredPayload details = 3;
     */
    details?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ErrorContent.
 * Use `create(ErrorContentSchema)` to create a new message.
 */
export declare const ErrorContentSchema: GenMessage<ErrorContent>;
/**
 * @generated from message proto.agent_workbench.v2.MediaContent
 */
export type MediaContent = Message<"proto.agent_workbench.v2.MediaContent"> & {
    /**
     * @generated from field: string artifact_id = 1;
     */
    artifactId: string;
    /**
     * @generated from field: string representation_id = 2;
     */
    representationId: string;
    /**
     * @generated from field: uint64 revision = 3;
     */
    revision: bigint;
    /**
     * @generated from field: string media_type = 4;
     */
    mediaType: string;
    /**
     * @generated from field: optional string role = 5;
     */
    role?: string | undefined;
    /**
     * @generated from field: optional string filename = 6;
     */
    filename?: string | undefined;
    /**
     * @generated from field: optional string alt_text = 7;
     */
    altText?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.MediaContent.
 * Use `create(MediaContentSchema)` to create a new message.
 */
export declare const MediaContentSchema: GenMessage<MediaContent>;
/**
 * @generated from message proto.agent_workbench.v2.ImageComparisonContent
 */
export type ImageComparisonContent = Message<"proto.agent_workbench.v2.ImageComparisonContent"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.MediaContent source = 1;
     */
    source?: MediaContent | undefined;
    /**
     * @generated from field: proto.agent_workbench.v2.MediaContent result = 2;
     */
    result?: MediaContent | undefined;
    /**
     * @generated from field: optional string default_mode = 3;
     */
    defaultMode?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.ImageComparisonContent.
 * Use `create(ImageComparisonContentSchema)` to create a new message.
 */
export declare const ImageComparisonContentSchema: GenMessage<ImageComparisonContent>;
/**
 * @generated from message proto.agent_workbench.v2.AnnotationContent
 */
export type AnnotationContent = Message<"proto.agent_workbench.v2.AnnotationContent"> & {
    /**
     * @generated from field: proto.agent_workbench.v2.MediaContent source = 1;
     */
    source?: MediaContent | undefined;
    /**
     * @generated from field: proto.agent_workbench.v2.StructuredPayload annotations = 2;
     */
    annotations?: StructuredPayload | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.AnnotationContent.
 * Use `create(AnnotationContentSchema)` to create a new message.
 */
export declare const AnnotationContentSchema: GenMessage<AnnotationContent>;
/**
 * @generated from message proto.agent_workbench.v2.HtmlContent
 */
export type HtmlContent = Message<"proto.agent_workbench.v2.HtmlContent"> & {
    /**
     * @generated from field: string security_profile = 1;
     */
    securityProfile: string;
    /**
     * @generated from oneof proto.agent_workbench.v2.HtmlContent.payload
     */
    payload: {
        /**
         * @generated from field: string source = 2;
         */
        value: string;
        case: "source";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MediaContent artifact = 3;
         */
        value: MediaContent;
        case: "artifact";
    } | {
        case: undefined;
        value?: undefined;
    };
};
/**
 * Describes the message proto.agent_workbench.v2.HtmlContent.
 * Use `create(HtmlContentSchema)` to create a new message.
 */
export declare const HtmlContentSchema: GenMessage<HtmlContent>;
/**
 * @generated from message proto.agent_workbench.v2.LivePreviewContent
 */
export type LivePreviewContent = Message<"proto.agent_workbench.v2.LivePreviewContent"> & {
    /**
     * @generated from field: string resource_id = 1;
     */
    resourceId: string;
    /**
     * @generated from field: string security_profile = 2;
     */
    securityProfile: string;
    /**
     * @generated from field: optional string session_url = 3;
     */
    sessionUrl?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.LivePreviewContent.
 * Use `create(LivePreviewContentSchema)` to create a new message.
 */
export declare const LivePreviewContentSchema: GenMessage<LivePreviewContent>;
/**
 * @generated from message proto.agent_workbench.v2.LinkContent
 */
export type LinkContent = Message<"proto.agent_workbench.v2.LinkContent"> & {
    /**
     * @generated from field: string url = 1;
     */
    url: string;
    /**
     * @generated from field: optional string label = 2;
     */
    label?: string | undefined;
    /**
     * @generated from field: optional string media_type = 3;
     */
    mediaType?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.LinkContent.
 * Use `create(LinkContentSchema)` to create a new message.
 */
export declare const LinkContentSchema: GenMessage<LinkContent>;
/**
 * @generated from message proto.agent_workbench.v2.CitationContent
 */
export type CitationContent = Message<"proto.agent_workbench.v2.CitationContent"> & {
    /**
     * @generated from field: string citation_id = 1;
     */
    citationId: string;
    /**
     * @generated from field: string label = 2;
     */
    label: string;
    /**
     * @generated from field: optional string url = 3;
     */
    url?: string | undefined;
    /**
     * @generated from field: optional string excerpt = 4;
     */
    excerpt?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.CitationContent.
 * Use `create(CitationContentSchema)` to create a new message.
 */
export declare const CitationContentSchema: GenMessage<CitationContent>;
/**
 * @generated from message proto.agent_workbench.v2.ArtifactReferenceContent
 */
export type ArtifactReferenceContent = Message<"proto.agent_workbench.v2.ArtifactReferenceContent"> & {
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
 * Describes the message proto.agent_workbench.v2.ArtifactReferenceContent.
 * Use `create(ArtifactReferenceContentSchema)` to create a new message.
 */
export declare const ArtifactReferenceContentSchema: GenMessage<ArtifactReferenceContent>;
/**
 * @generated from message proto.agent_workbench.v2.RestrictedIframeContent
 */
export type RestrictedIframeContent = Message<"proto.agent_workbench.v2.RestrictedIframeContent"> & {
    /**
     * @generated from field: string renderer_id = 1;
     */
    rendererId: string;
    /**
     * @generated from field: string protocol_version = 2;
     */
    protocolVersion: string;
    /**
     * @generated from field: string url = 3;
     */
    url: string;
    /**
     * @generated from field: string origin = 4;
     */
    origin: string;
    /**
     * @generated from field: repeated string sandbox_tokens = 5;
     */
    sandboxTokens: string[];
    /**
     * @generated from field: repeated string permissions = 6;
     */
    permissions: string[];
    /**
     * @generated from field: uint64 max_inbound_bytes = 7;
     */
    maxInboundBytes: bigint;
    /**
     * @generated from field: uint64 max_outbound_bytes = 8;
     */
    maxOutboundBytes: bigint;
};
/**
 * Describes the message proto.agent_workbench.v2.RestrictedIframeContent.
 * Use `create(RestrictedIframeContentSchema)` to create a new message.
 */
export declare const RestrictedIframeContentSchema: GenMessage<RestrictedIframeContent>;
/**
 * @generated from message proto.agent_workbench.v2.ContentBlock
 */
export type ContentBlock = Message<"proto.agent_workbench.v2.ContentBlock"> & {
    /**
     * @generated from field: string content_id = 1;
     */
    contentId: string;
    /**
     * @generated from field: proto.agent_workbench.v2.ContentIdentity identity = 2;
     */
    identity?: ContentIdentity | undefined;
    /**
     * @generated from oneof proto.agent_workbench.v2.ContentBlock.content
     */
    content: {
        /**
         * @generated from field: proto.agent_workbench.v2.TextContent text = 10;
         */
        value: TextContent;
        case: "text";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MarkdownContent markdown = 11;
         */
        value: MarkdownContent;
        case: "markdown";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.CodeContent code = 12;
         */
        value: CodeContent;
        case: "code";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.JsonContent json = 13;
         */
        value: JsonContent;
        case: "json";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.TableContent table = 14;
         */
        value: TableContent;
        case: "table";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.DiffContent diff = 15;
         */
        value: DiffContent;
        case: "diff";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.CommandContent command = 16;
         */
        value: CommandContent;
        case: "command";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.LogContent log = 17;
         */
        value: LogContent;
        case: "log";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ProgressContent progress = 18;
         */
        value: ProgressContent;
        case: "progress";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ErrorContent error = 19;
         */
        value: ErrorContent;
        case: "error";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MediaContent image = 20;
         */
        value: MediaContent;
        case: "image";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ImageComparisonContent image_comparison = 21;
         */
        value: ImageComparisonContent;
        case: "imageComparison";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.AnnotationContent annotation = 22;
         */
        value: AnnotationContent;
        case: "annotation";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MediaContent video = 23;
         */
        value: MediaContent;
        case: "video";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MediaContent audio = 24;
         */
        value: MediaContent;
        case: "audio";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.HtmlContent html = 25;
         */
        value: HtmlContent;
        case: "html";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.LivePreviewContent live_preview = 26;
         */
        value: LivePreviewContent;
        case: "livePreview";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MediaContent pdf = 27;
         */
        value: MediaContent;
        case: "pdf";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MediaContent presentation = 28;
         */
        value: MediaContent;
        case: "presentation";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MediaContent spreadsheet = 29;
         */
        value: MediaContent;
        case: "spreadsheet";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.MediaContent file = 30;
         */
        value: MediaContent;
        case: "file";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.LinkContent link = 31;
         */
        value: LinkContent;
        case: "link";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.CitationContent citation = 32;
         */
        value: CitationContent;
        case: "citation";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.ArtifactReferenceContent artifact_ref = 33;
         */
        value: ArtifactReferenceContent;
        case: "artifactRef";
    } | {
        /**
         * @generated from field: proto.agent_workbench.v2.RestrictedIframeContent restricted_iframe = 34;
         */
        value: RestrictedIframeContent;
        case: "restrictedIframe";
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
 * Describes the message proto.agent_workbench.v2.ContentBlock.
 * Use `create(ContentBlockSchema)` to create a new message.
 */
export declare const ContentBlockSchema: GenMessage<ContentBlock>;
/**
 * @generated from enum proto.agent_workbench.v2.UnsupportedReason
 */
export declare enum UnsupportedReason {
    /**
     * @generated from enum value: UNSUPPORTED_REASON_UNSPECIFIED = 0;
     */
    UNSPECIFIED = 0,
    /**
     * @generated from enum value: UNSUPPORTED_REASON_UNKNOWN = 1;
     */
    UNKNOWN = 1,
    /**
     * @generated from enum value: UNSUPPORTED_REASON_UNSUPPORTED = 2;
     */
    UNSUPPORTED = 2,
    /**
     * @generated from enum value: UNSUPPORTED_REASON_INVALID = 3;
     */
    INVALID = 3
}
/**
 * Describes the enum proto.agent_workbench.v2.UnsupportedReason.
 */
export declare const UnsupportedReasonSchema: GenEnum<UnsupportedReason>;
