export interface KnowledgeAnswerSource {
    excerpt?: string;
    id: string;
    label: string;
    url?: string;
}
export interface SplitKnowledgeAnswer {
    body: string;
    showDisclaimer: boolean;
    sources: KnowledgeAnswerSource[];
}
export declare function splitKnowledgeAnswer(text: string): SplitKnowledgeAnswer;
