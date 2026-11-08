import { type RefObject } from "react";
export declare function useJumpToLatest(el: RefObject<HTMLElement | null>, itemCount: number, sessionId: string): {
    showJump: boolean;
    jumpToLatest: () => void;
};
