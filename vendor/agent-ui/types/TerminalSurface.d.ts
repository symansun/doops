import "@xterm/xterm/css/xterm.css";
import type { TerminalResource, TerminalRuntime } from "./contracts.js";
import { type TerminalMobileToolbarRenderer } from "./TerminalMobileToolbar.js";
export declare function TerminalSurface({ clientLabel, mobile, mobileToolbar, resource, runtime, }: {
    clientLabel: string;
    mobile?: boolean;
    mobileToolbar?: TerminalMobileToolbarRenderer;
    resource: TerminalResource;
    runtime: TerminalRuntime;
}): import("react/jsx-runtime").JSX.Element;
