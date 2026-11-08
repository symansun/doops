import { EditorView } from "@codemirror/view";
import type { WorkspaceCodeLanguage } from "./workspaceCodeLanguage.js";
export interface ReadonlyCodeMirrorHandle {
    view: EditorView;
    setDoc(next: string): void;
    setLanguage(language: WorkspaceCodeLanguage, highlight: boolean): Promise<void>;
    openFind(): void;
    revealLine(line: number): void;
    selection(): {
        from: number;
        text: string;
        to: number;
    } | null;
    destroy(): void;
}
export declare function mountReadonlyEditor(input: {
    parent: HTMLElement;
    doc: string;
    language: WorkspaceCodeLanguage;
    highlight: boolean;
}): Promise<ReadonlyCodeMirrorHandle>;
