import { type Event } from "../generated/events/v1/events_pb.js";
export interface EventsConnectTransportOptions {
    baseUrl: string;
    getAccessToken: () => Promise<string> | string;
    tenantId: string;
    fetch?: typeof globalThis.fetch;
}
export declare class EventsConnectTransport {
    private readonly client;
    private readonly tenantId;
    constructor(options: EventsConnectTransportOptions);
    subscribe(signal?: AbortSignal): AsyncIterable<Event>;
}
