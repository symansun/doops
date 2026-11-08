export interface MentionState {
    query: string;
    start: number;
    end: number;
}
export declare function detectMentionAt(text: string, caret: number): MentionState | null;
export declare function parseMentionToken(query: string): {
    dir: string;
    filter: string;
};
