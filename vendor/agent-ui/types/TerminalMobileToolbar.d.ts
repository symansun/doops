import type { ReactNode } from "react";
export interface TerminalMobileToolbarActions {
    disabled: boolean;
    send: (input: string) => void;
}
export type TerminalMobileToolbarRenderer = (actions: TerminalMobileToolbarActions) => ReactNode;
export declare function TerminalMobileToolbar({ disabled, send, }: TerminalMobileToolbarActions): import("react/jsx-runtime").JSX.Element;
