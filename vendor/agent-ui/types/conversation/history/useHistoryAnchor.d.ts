import { type RefObject } from "react";
/**
 * Preserve visual position when older items are prepended. Capture must happen
 * during render (before paint of the new items); restore in useLayoutEffect.
 */
export declare function useHistoryAnchor(el: RefObject<HTMLElement | null>, itemCount: number, loadingOlder: boolean): void;
