import type { AgentCommand } from "./contracts.js";
export declare function ComposerCommandMenu({ commands, onSelect, query, }: {
    commands: AgentCommand[];
    onSelect: (command: AgentCommand) => void;
    query: string | null;
}): import("react/jsx-runtime").JSX.Element | null;
export declare function commandQuery(value: string): string | null;
export declare function parseAgentCommand(value: string, commands: AgentCommand[]): {
    command: AgentCommand;
    arguments: string;
} | null;
