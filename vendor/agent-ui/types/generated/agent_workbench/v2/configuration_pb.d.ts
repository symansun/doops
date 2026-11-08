import type { GenFile, GenMessage } from "@bufbuild/protobuf/codegenv2";
import type { Message } from "@bufbuild/protobuf";
/**
 * Describes the file agent_workbench/v2/configuration.proto.
 */
export declare const file_agent_workbench_v2_configuration: GenFile;
/**
 * @generated from message proto.agent_workbench.v2.SessionConfiguration
 */
export type SessionConfiguration = Message<"proto.agent_workbench.v2.SessionConfiguration"> & {
    /**
     * @generated from field: optional string model = 1;
     */
    model?: string | undefined;
    /**
     * @generated from field: optional string permission_mode = 2;
     */
    permissionMode?: string | undefined;
};
/**
 * Describes the message proto.agent_workbench.v2.SessionConfiguration.
 * Use `create(SessionConfigurationSchema)` to create a new message.
 */
export declare const SessionConfigurationSchema: GenMessage<SessionConfiguration>;
