import { type RefObject } from "react";
export declare const LOAD_OLDER_TRIGGER_PX = 300;
export declare function useLoadOlderTrigger(el: RefObject<HTMLElement | null>, hasOlder: boolean, loadOlder: () => Promise<void>, itemCount?: number): void;
