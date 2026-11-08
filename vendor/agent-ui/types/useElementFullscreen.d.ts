import { type RefObject } from "react";
export declare function useElementFullscreen(target: RefObject<HTMLElement | null>): {
    active: boolean;
    supported: boolean;
    toggle: () => void;
};
