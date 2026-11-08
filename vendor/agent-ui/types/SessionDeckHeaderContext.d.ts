interface SessionDeckHeaderContextValue {
    controls: HTMLDivElement | null;
    inDeck: boolean;
}
export declare const SessionDeckHeaderContext: import("react").Context<SessionDeckHeaderContextValue>;
export declare function useSessionDeckHeader(): SessionDeckHeaderContextValue;
export {};
