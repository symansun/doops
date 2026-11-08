import type { Extension } from "@codemirror/state";
import type { WorkspaceCodeLanguage } from "./workspaceCodeLanguage.js";
export declare function languageExtension(language: WorkspaceCodeLanguage): Promise<Extension>;
