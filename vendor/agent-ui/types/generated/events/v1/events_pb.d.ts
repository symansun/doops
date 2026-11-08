import type { GenFile, GenMessage, GenService } from "@bufbuild/protobuf/codegenv2";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file events/v1/events.proto.
 */
export declare const file_events_v1_events: GenFile;
/**
 * @generated from message proto.events.v1.SubscribeRequest
 */
export type SubscribeRequest = Message<"proto.events.v1.SubscribeRequest"> & {
    /**
     * @generated from field: string tenant_id = 1;
     */
    tenantId: string;
    /**
     * Optional event-type allowlist. Empty (or absent) = all types. The
     * server MAY ignore this filter for simplicity; it exists for future
     * bandwidth optimization without a proto bump.
     *
     * @generated from field: repeated string event_types = 2;
     */
    eventTypes: string[];
};
/**
 * Describes the message proto.events.v1.SubscribeRequest.
 * Use `create(SubscribeRequestSchema)` to create a new message.
 */
export declare const SubscribeRequestSchema: GenMessage<SubscribeRequest>;
/**
 * Event mirrors the legacy WebSocket payload shape from
 * backend/internal/infra/eventbus/types.go. `data_json` keeps the
 * per-event-type payload free-form (see header comment).
 *
 * @generated from message proto.events.v1.Event
 */
export type Event = Message<"proto.events.v1.Event"> & {
    /**
     * @generated from field: string type = 1;
     */
    type: string;
    /**
     * "entity" | "notification" | "system"
     *
     * @generated from field: string category = 2;
     */
    category: string;
    /**
     * @generated from field: int64 organization_id = 3;
     */
    organizationId: bigint;
    /**
     * @generated from field: optional int64 target_user_id = 4;
     */
    targetUserId?: bigint | undefined;
    /**
     * @generated from field: repeated int64 target_user_ids = 5;
     */
    targetUserIds: bigint[];
    /**
     * @generated from field: optional string entity_type = 6;
     */
    entityType?: string | undefined;
    /**
     * @generated from field: optional string entity_id = 7;
     */
    entityId?: string | undefined;
    /**
     * JSON-encoded payload — schema depends on `type`.
     *
     * @generated from field: string data_json = 8;
     */
    dataJson: string;
    /**
     * Unix milliseconds.
     *
     * @generated from field: int64 timestamp = 9;
     */
    timestamp: bigint;
    /**
     * Liveness sentinel marker: the server's ready/keepalive frames set this and
     * carry no business payload. A dedicated field (not a reserved `type` value)
     * keeps liveness orthogonal to the business type namespace, so no business
     * event can collide with a sentinel; the client flips Connected / refreshes
     * idle on these frames but never dispatches them.
     *
     * @generated from field: bool keepalive = 10;
     */
    keepalive: boolean;
};
/**
 * Describes the message proto.events.v1.Event.
 * Use `create(EventSchema)` to create a new message.
 */
export declare const EventSchema: GenMessage<Event>;
/**
 * @generated from service proto.events.v1.EventsService
 */
export declare const EventsService: GenService<{
    /**
     * Subscribes to org-scoped real-time events. The stream stays open
     * until the client cancels or the server closes it (auth failure,
     * server shutdown, etc.).
     *
     * @generated from rpc proto.events.v1.EventsService.Subscribe
     */
    subscribe: {
        methodKind: "server_streaming";
        input: typeof SubscribeRequestSchema;
        output: typeof EventSchema;
    };
}>;
