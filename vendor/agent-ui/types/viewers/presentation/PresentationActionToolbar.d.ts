import { type PresentationGrant } from "./presentationContracts.js";
export declare function PresentationActionToolbar({ currentIndex, grants, onExport, onMove, onRegenerate, onReplace, slideCount, }: {
    currentIndex: number;
    grants: readonly PresentationGrant[];
    onExport: () => void;
    onMove: (targetIndex: number) => void;
    onRegenerate: () => void;
    onReplace: () => void;
    slideCount: number;
}): import("react/jsx-runtime").JSX.Element;
