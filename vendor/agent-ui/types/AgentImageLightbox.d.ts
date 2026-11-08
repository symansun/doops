import { type ReactNode } from "react";
export interface AgentLightboxImage {
    alt: string;
    src: string;
}
interface LightboxContextValue {
    open: (image: AgentLightboxImage) => void;
}
export interface ImageLightboxProviderProps {
    children: ReactNode;
    portalContainer?: HTMLElement | null;
}
export interface ZoomableImageProps extends React.ComponentProps<"img"> {
    alt: string;
    src?: string;
}
export declare function useLightbox(): LightboxContextValue;
export declare function ZoomableImage({ alt, className, src, ...imgProps }: ZoomableImageProps): import("react/jsx-runtime").JSX.Element;
export declare function ImageLightboxProvider({ children, portalContainer, }: ImageLightboxProviderProps): import("react/jsx-runtime").JSX.Element;
export {};
