import { type ReactNode } from "react";
export interface WorkspaceFileReveal {
    line?: number;
    path: string;
}
type RevealHandler = (target: WorkspaceFileReveal) => void;
interface WorkspaceFileNavigation {
    reveal: RevealHandler;
    setHandler: (handler: RevealHandler | null) => void;
    setWorkspaceRoot: (root: string | undefined) => void;
    workspaceRoot: () => string | undefined;
}
export declare function WorkspaceFileNavigationProvider({ children, onReveal, }: {
    children: ReactNode;
    onReveal?: () => void;
}): import("react/jsx-runtime").JSX.Element;
export declare function useWorkspaceFileNavigation(): WorkspaceFileNavigation | null;
export {};
