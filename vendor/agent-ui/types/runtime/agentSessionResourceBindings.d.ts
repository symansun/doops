import type { AgentSessionRuntime } from "../contracts.js";
import type { AgentSessionRuntimeV2Options } from "./agentSessionRuntimeOptions.js";
export declare function bindSessionResources(options: AgentSessionRuntimeV2Options, requireSession: (id: string) => void): Pick<AgentSessionRuntime, "loadAttachment" | "loadArtifact" | "loadPreviewUrl" | "uploadAttachment">;
