import type { WorkspaceCodeLanguage } from "./workspaceCodeLanguage.js";
export declare function mountMergeDiff(input: {
    after: string;
    before: string;
    highlight: boolean;
    language: WorkspaceCodeLanguage;
    parent: HTMLElement;
    unified: boolean;
}): Promise<{
    destroy(): void;
}>;
