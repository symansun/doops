export declare const STATIC_HTML_SANDBOX = "";
export declare const STATIC_HTML_REFERRER_POLICY = "no-referrer";
export declare const STATIC_HTML_CSP: string;
type StaticHtmlWindowOpenResult = {
    opened: true;
} | {
    opened: false;
    reason: "popup-blocked";
};
export declare function staticHtmlDocument(html: string): string;
export declare function openStaticHtmlInNewWindow(html: string, title?: string, opener?: Pick<Window, "open">): StaticHtmlWindowOpenResult;
export {};
