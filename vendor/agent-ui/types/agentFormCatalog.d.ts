export declare const agentFormCatalog: {
    identity: {
        namespace: string;
        semantic_key: string;
        schema_version: string;
    };
    components: {
        Panel: {
            description: string;
            children: boolean;
            props: {
                type: string;
                additionalProperties: boolean;
                properties: {};
            };
        };
        SchemaForm: {
            description: string;
            children: boolean;
            props: {
                type: string;
                additionalProperties: boolean;
                properties: {};
            };
        };
        FieldIssues: {
            description: string;
            children: boolean;
            props: {
                type: string;
                additionalProperties: boolean;
                properties: {};
            };
        };
        NoticeDialog: {
            description: string;
            children: boolean;
            props: {
                type: string;
                additionalProperties: boolean;
                required: string[];
                properties: {
                    title: {
                        type: string;
                        minLength: number;
                        maxLength: number;
                    };
                    trigger: {
                        type: string;
                        minLength: number;
                        maxLength: number;
                    };
                    paragraphs: {
                        type: string;
                        minItems: number;
                        maxItems: number;
                        items: {
                            type: string;
                            minLength: number;
                            maxLength: number;
                        };
                    };
                };
            };
        };
        WorkflowActions: {
            description: string;
            children: boolean;
            props: {
                type: string;
                additionalProperties: boolean;
                properties: {};
            };
        };
    };
    limits: {
        payload_bytes: number;
        nodes: number;
        children: number;
        schema_nodes: number;
        schema_depth: number;
    };
    schema_policy: {
        dialect: string;
        forbidden_keywords: string[];
        widgets: string[];
        formats: string[];
    };
    surface_schema: {
        $schema: string;
        type: string;
        additionalProperties: boolean;
        required: string[];
        properties: {
            kind: {
                const: string;
            };
            app_slug: {
                type: string;
                minLength: number;
                maxLength: number;
                pattern: string;
            };
            form_id: {
                type: string;
                minLength: number;
                maxLength: number;
                pattern: string;
            };
            instance_id: {
                type: string;
                minLength: number;
                maxLength: number;
                pattern: string;
            };
            data_revision: {
                type: string;
                minimum: number;
                maximum: number;
            };
            data: {
                type: string;
            };
            schema: {
                type: string;
                required: string[];
                properties: {
                    type: {
                        const: string;
                    };
                };
            };
            ui_schema: {
                type: string;
            };
            read_only: {
                type: string;
            };
            actions: {
                type: string;
                maxItems: number;
                items: {
                    type: string;
                    additionalProperties: boolean;
                    required: string[];
                    properties: {
                        id: {
                            type: string;
                            minLength: number;
                            maxLength: number;
                            pattern: string;
                        };
                        label: {
                            type: string;
                            minLength: number;
                            maxLength: number;
                        };
                        behavior: {
                            enum: string[];
                        };
                        confirmation: {
                            type: string;
                            additionalProperties: boolean;
                            required: string[];
                            properties: {
                                title: {
                                    type: string;
                                    minLength: number;
                                    maxLength: number;
                                };
                                description: {
                                    type: string;
                                    minLength: number;
                                    maxLength: number;
                                };
                                confirmLabel: {
                                    type: string;
                                    minLength: number;
                                    maxLength: number;
                                };
                            };
                        };
                    };
                };
            };
            spec: {
                type: string;
            };
            errors: {
                type: string;
                additionalProperties: boolean;
                required: string[];
                properties: {
                    data_revision: {
                        type: string;
                        minimum: number;
                        maximum: number;
                    };
                    fields: {
                        type: string;
                        maxItems: number;
                        items: {
                            type: string;
                            additionalProperties: boolean;
                            required: string[];
                            properties: {
                                field: {
                                    type: string;
                                    maxLength: number;
                                };
                                description: {
                                    type: string;
                                    maxLength: number;
                                };
                            };
                        };
                    };
                };
            };
            workflow: {
                type: string;
                additionalProperties: boolean;
                required: string[];
                properties: {
                    notice: {
                        type: string;
                        additionalProperties: boolean;
                        required: string[];
                        properties: {
                            id: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                                pattern: string;
                            };
                            version: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                            };
                            title: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                            };
                            paragraphs: {
                                type: string;
                                minItems: number;
                                maxItems: number;
                                items: {
                                    type: string;
                                    minLength: number;
                                    maxLength: number;
                                };
                            };
                        };
                    };
                    pledge: {
                        type: string;
                        additionalProperties: boolean;
                        required: string[];
                        properties: {
                            id: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                                pattern: string;
                            };
                            version: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                            };
                            title: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                            };
                            paragraphs: {
                                type: string;
                                minItems: number;
                                maxItems: number;
                                items: {
                                    type: string;
                                    minLength: number;
                                    maxLength: number;
                                };
                            };
                        };
                    };
                    actions: {
                        type: string;
                        additionalProperties: boolean;
                        required: string[];
                        properties: {
                            accept: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                                pattern: string;
                            };
                            decline: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                                pattern: string;
                            };
                            save: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                                pattern: string;
                            };
                            sign: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                                pattern: string;
                            };
                            cancel: {
                                type: string;
                                minLength: number;
                                maxLength: number;
                                pattern: string;
                            };
                        };
                    };
                };
            };
        };
    };
    sections_schema: {
        type: string;
        minItems: number;
        maxItems: number;
        items: {
            type: string;
            additionalProperties: boolean;
            required: string[];
            properties: {
                title: {
                    type: string;
                    minLength: number;
                    maxLength: number;
                };
                fields: {
                    type: string;
                    minItems: number;
                    maxItems: number;
                    items: {
                        type: string;
                        minLength: number;
                        maxLength: number;
                    };
                };
                collapsed: {
                    type: string;
                };
                summaryFields: {
                    type: string;
                    maxItems: number;
                    items: {
                        type: string;
                        minLength: number;
                        maxLength: number;
                    };
                };
            };
        };
    };
    application_actions: {
        "vehicle-booking/booking": string[];
    };
};
