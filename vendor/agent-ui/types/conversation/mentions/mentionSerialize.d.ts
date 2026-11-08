export interface MentionItem {
    path: string;
    isDir: boolean;
    lineRange?: {
        start: number;
        end: number;
    };
}
export declare function mentionItemKey(item: MentionItem): string;
export declare function mentionItemPath(item: MentionItem): string;
export declare function mentionMarkerFor(harness: string | null, path: string): string;
export declare function buildMentionPreamble(items: readonly MentionItem[], harness: string | null): string;
