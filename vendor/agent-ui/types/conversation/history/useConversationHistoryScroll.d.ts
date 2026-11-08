import { type RefObject } from "react";
export declare function useConversationHistoryScroll(options: {
    hasOlderItems: boolean;
    itemCount: number;
    historyItemCount?: number;
    loadOlder: () => Promise<void>;
    sessionId: string;
}): {
    loadingOlder: boolean;
    scrollRef: RefObject<HTMLElement | null>;
    showJump: boolean;
    jumpToLatest: () => void;
};
