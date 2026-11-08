declare const FILLED: {
    readonly scroll: "min-h-0 w-full flex-1 overflow-auto border-b border-border";
    readonly media: "min-h-0 w-full flex-1 bg-muted object-contain";
    readonly frame: "min-h-0 w-full flex-1 border-b border-border bg-white";
    readonly shell: "flex min-h-0 w-full flex-1 items-center justify-center border-b border-border";
};
export declare function previewPaneClass(fill: boolean, compact: string, surface?: keyof typeof FILLED): string;
export {};
