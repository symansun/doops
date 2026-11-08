export type WorkbenchContainerMode = "narrow" | "medium" | "wide";
export declare function workbenchContainerMode(width: number): WorkbenchContainerMode;
export declare function useWorkbenchContainerMode(): {
    containerRef: import("react").RefObject<HTMLDivElement | null>;
    mode: WorkbenchContainerMode;
};
