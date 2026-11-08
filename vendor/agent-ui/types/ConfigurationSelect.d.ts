import type { AgentConfigurationOption } from "./contracts.js";
export declare function ConfigurationSelect({ disabled, label, loading, onChange, options, optionsLabel, placeholder, value, }: {
    disabled: boolean;
    label: string;
    loading?: boolean;
    onChange: (value: string) => void;
    options: AgentConfigurationOption[];
    optionsLabel: string;
    placeholder: string;
    value: string;
}): import("react/jsx-runtime").JSX.Element;
