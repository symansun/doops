import type { PresentationSlide } from "./presentationContracts.js";
export declare function PresentationThumbnailRail({ activeSlideId, onSelect, slides, }: {
    activeSlideId?: string;
    onSelect: (slideId: string) => void;
    slides: readonly PresentationSlide[];
}): import("react/jsx-runtime").JSX.Element;
