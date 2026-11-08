import { serviceDesc, messageDesc, fileDesc } from "@bufbuild/protobuf/codegenv2";
import { br as file_agent_workbench_v2_command, bu as file_agent_workbench_v2_session, bc as ToolIdentitySchema } from "./session_pb-CU4zY4LI.js";
import { create } from "@bufbuild/protobuf";
const file_agent_workbench_v2_service = /* @__PURE__ */ fileDesc("CiBhZ2VudF93b3JrYmVuY2gvdjIvc2VydmljZS5wcm90bxIYcHJvdG8uYWdlbnRfd29ya2JlbmNoLnYyIlkKGUdldFNlc3Npb25TbmFwc2hvdFJlcXVlc3QSEQoJdGVuYW50X2lkGAEgASgJEhIKCnNlc3Npb25faWQYAiABKAkSFQoNaGlzdG9yeV9saW1pdBgDIAEoDSJ+ChpTdHJlYW1TZXNzaW9uRGVsdGFzUmVxdWVzdBIRCgl0ZW5hbnRfaWQYASABKAkSNwoGY3Vyc29yGAIgASgLMicucHJvdG8uYWdlbnRfd29ya2JlbmNoLnYyLlNlc3Npb25DdXJzb3ISFAoMcmVwbGF5X2xpbWl0GAMgASgNImYKFUV4ZWN1dGVDb21tYW5kUmVxdWVzdBIRCgl0ZW5hbnRfaWQYASABKAkSOgoHY29tbWFuZBgCIAEoCzIpLnByb3RvLmFnZW50X3dvcmtiZW5jaC52Mi5Db21tYW5kRW52ZWxvcGUy9gIKFUFnZW50V29ya2JlbmNoU2VydmljZRJ0ChJHZXRTZXNzaW9uU25hcHNob3QSMy5wcm90by5hZ2VudF93b3JrYmVuY2gudjIuR2V0U2Vzc2lvblNuYXBzaG90UmVxdWVzdBopLnByb3RvLmFnZW50X3dvcmtiZW5jaC52Mi5TZXNzaW9uU25hcHNob3QSegoTU3RyZWFtU2Vzc2lvbkRlbHRhcxI0LnByb3RvLmFnZW50X3dvcmtiZW5jaC52Mi5TdHJlYW1TZXNzaW9uRGVsdGFzUmVxdWVzdBorLnByb3RvLmFnZW50X3dvcmtiZW5jaC52Mi5TZXNzaW9uRGVsdGFCYXRjaDABEmsKDkV4ZWN1dGVDb21tYW5kEi8ucHJvdG8uYWdlbnRfd29ya2JlbmNoLnYyLkV4ZWN1dGVDb21tYW5kUmVxdWVzdBooLnByb3RvLmFnZW50X3dvcmtiZW5jaC52Mi5Db21tYW5kUmVjZWlwdEJQWk5naXRodWIuY29tL2w4YWktY24vYWdlbnRjbG91ZC9wcm90by9nZW4vZ28vYWdlbnRfd29ya2JlbmNoL3YyO2FnZW50d29ya2JlbmNodjJiBnByb3RvMw", [file_agent_workbench_v2_command, file_agent_workbench_v2_session]);
const GetSessionSnapshotRequestSchema = /* @__PURE__ */ messageDesc(file_agent_workbench_v2_service, 0);
const StreamSessionDeltasRequestSchema = /* @__PURE__ */ messageDesc(file_agent_workbench_v2_service, 1);
const ExecuteCommandRequestSchema = /* @__PURE__ */ messageDesc(file_agent_workbench_v2_service, 2);
const AgentWorkbenchService = /* @__PURE__ */ serviceDesc(file_agent_workbench_v2_service, 0);
const sourceToolCatalog = /* @__PURE__ */ new Map([
  [
    "acp",
    /* @__PURE__ */ new Map([
      ["Bash", { semanticKey: "shell.execute", schemaVersion: "1" }],
      ["Read", { semanticKey: "filesystem.read", schemaVersion: "1" }],
      ["Write", { semanticKey: "filesystem.write", schemaVersion: "1" }],
      ["Edit", { semanticKey: "filesystem.edit", schemaVersion: "1" }],
      ["Grep", { semanticKey: "filesystem.search", schemaVersion: "1" }],
      ["Glob", { semanticKey: "filesystem.search", schemaVersion: "1" }],
      ["LS", { semanticKey: "filesystem.search", schemaVersion: "1" }],
      ["list_files", { semanticKey: "filesystem.search", schemaVersion: "1" }],
      ["WebFetch", { semanticKey: "web.fetch", schemaVersion: "1" }],
      [
        "AskUserQuestion",
        { semanticKey: "interaction.question", schemaVersion: "1" }
      ],
      ["shell", { semanticKey: "shell.execute", schemaVersion: "1" }],
      ["fileChange", { semanticKey: "filesystem.change", schemaVersion: "1" }],
      ["ExecuteCode", { semanticKey: "code.interpret", schemaVersion: "1" }],
      [
        "image_generation",
        { semanticKey: "media.image.generate", schemaVersion: "1" }
      ]
    ])
  ],
  [
    "claude",
    /* @__PURE__ */ new Map([
      ["Bash", { semanticKey: "shell.execute", schemaVersion: "1" }],
      ["Read", { semanticKey: "filesystem.read", schemaVersion: "1" }],
      ["Write", { semanticKey: "filesystem.write", schemaVersion: "1" }],
      ["Edit", { semanticKey: "filesystem.edit", schemaVersion: "1" }],
      ["Grep", { semanticKey: "filesystem.search", schemaVersion: "1" }],
      ["Glob", { semanticKey: "filesystem.search", schemaVersion: "1" }],
      ["LS", { semanticKey: "filesystem.search", schemaVersion: "1" }],
      ["list_files", { semanticKey: "filesystem.search", schemaVersion: "1" }],
      ["WebFetch", { semanticKey: "web.fetch", schemaVersion: "1" }],
      ["ExecuteCode", { semanticKey: "code.interpret", schemaVersion: "1" }],
      [
        "AskUserQuestion",
        { semanticKey: "interaction.question", schemaVersion: "1" }
      ]
    ])
  ],
  [
    "codex",
    /* @__PURE__ */ new Map([
      ["Bash", { semanticKey: "shell.execute", schemaVersion: "1" }],
      ["Read", { semanticKey: "filesystem.read", schemaVersion: "1" }],
      ["Write", { semanticKey: "filesystem.write", schemaVersion: "1" }],
      ["shell", { semanticKey: "shell.execute", schemaVersion: "1" }],
      ["fileChange", { semanticKey: "filesystem.change", schemaVersion: "1" }],
      [
        "image_generation",
        { semanticKey: "media.image.generate", schemaVersion: "1" }
      ],
      // Codex CLI 以 OpenAI Responses item 形式上报这组工具调用
      ["web_search_call", { semanticKey: "web.search", schemaVersion: "1" }],
      [
        "file_search_call",
        { semanticKey: "filesystem.search", schemaVersion: "1" }
      ],
      [
        "code_interpreter_call",
        { semanticKey: "code.interpret", schemaVersion: "1" }
      ],
      ["computer_call", { semanticKey: "computer.use", schemaVersion: "1" }],
      [
        "image_generation_call",
        { semanticKey: "media.image.generate", schemaVersion: "1" }
      ],
      ["mcp_call", { semanticKey: "mcp.call", schemaVersion: "1" }],
      ["mcp_list_tools", { semanticKey: "mcp.list-tools", schemaVersion: "1" }]
    ])
  ]
]);
function resolveSourceTool(sourceProtocol, exactSourceToolName) {
  const descriptor = sourceToolCatalog.get(sourceProtocol)?.get(exactSourceToolName);
  if (!descriptor) {
    return void 0;
  }
  return create(ToolIdentitySchema, {
    namespace: `agentcloud.${sourceProtocol}`,
    semanticKey: descriptor.semanticKey,
    schemaVersion: descriptor.schemaVersion,
    sourceToolName: exactSourceToolName
  });
}
export {
  AgentWorkbenchService as A,
  ExecuteCommandRequestSchema as E,
  GetSessionSnapshotRequestSchema as G,
  StreamSessionDeltasRequestSchema as S,
  file_agent_workbench_v2_service as f,
  resolveSourceTool as r
};
