export declare const MENTION_MATCH_CAP = 50;
export declare function rankMentionEntries<T extends {
    name: string;
    type: string;
}>(entries: readonly T[], filter: string, cap?: number): T[];
