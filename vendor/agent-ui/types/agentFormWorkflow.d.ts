export interface AgreementDocument {
    id: string;
    version: string;
    title: string;
    paragraphs: string[];
}
export interface AgentFormWorkflow {
    notice: AgreementDocument;
    pledge: AgreementDocument;
    actions: {
        accept: string;
        decline: string;
        save: string;
        sign: string;
        cancel: string;
    };
}
export interface FormGateEvidence {
    document_id: string;
    document_version: string;
    acknowledged: boolean;
    signer?: string;
}
export interface FormGateRecord {
    status: "accepted" | "declined";
    at: string;
    actor_id: number;
    organization_id: number;
    booking_digest?: string;
    signer?: string;
    document?: AgreementDocument;
    document_digest?: string;
}
export interface AgentDatePolicy {
    configured: boolean;
    message: string;
    evaluated_at: string;
    earliest_local?: string;
    earliest_remote?: string;
    calendar?: {
        version: string;
        source: string;
        home_city: string;
        range_start: string;
        range_end: string;
        accept_from: string;
        accept_until: string;
    };
}
export declare function parseFormWorkflow(value: unknown): AgentFormWorkflow | undefined;
