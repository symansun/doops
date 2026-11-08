import type { PresentationSlide } from "./presentationContracts.js";
export declare function PresentationStage({ currentIndex, currentSlide, onRequestFullscreen, slideCount, }: {
    currentIndex: number;
    currentSlide?: PresentationSlide;
    onRequestFullscreen?: () => void | Promise<void>;
    slideCount: number;
}): import("react/jsx-runtime").JSX.Element;
