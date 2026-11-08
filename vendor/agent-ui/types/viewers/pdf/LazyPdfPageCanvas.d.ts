import { type RefObject } from "react";
import type { PDFDocumentProxy } from "pdfjs-dist";
export declare function LazyPdfPageCanvas({ document, filename, pageNumber, scrollRootRef, }: {
    document: PDFDocumentProxy;
    filename: string;
    pageNumber: number;
    scrollRootRef: RefObject<HTMLDivElement | null>;
}): import("react/jsx-runtime").JSX.Element;
