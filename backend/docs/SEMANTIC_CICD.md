# Semantic CI/CD

DoOps v3 has one user-facing release path:

```text
natural-language task
  -> explicit target
  -> explicit --allow-mutate authorization
  -> Agent build mode
  -> runtime tool calls
  -> workload and health readback
```

There is no declarative deployment root, generated workflow, temporary source
snapshot, immutable commit binding, or second orchestration RPC.

## Request Contract

The task must state:

- target;
- service scope;
- image version or digest;
- reason;
- acceptance checks;
- mutation authorization.

Missing fields block execution. The Agent must not infer them from directory
names, domains, historical releases, or legacy declarations.

## Execution

Read-only analysis uses:

```bash
doops -session <session> ask --target <target> --msg '<task>'
```

Authorized mutation uses:

```bash
doops -session <session> ask --allow-mutate --target <target> --msg '<task>'
```

Mutation asks require Gateway exec permission, run in a fresh temporary working
directory, and remove that directory after the turn. They do not create or
reuse `/root/ws` release snapshots.

## Result

The CLI returns the Agent's actual final message. A completed mutation turn
with no completed tool call fails as `doagent_apply_no_tool_activity`.
Successful release text is accepted only after tool execution and explicit
runtime readback. Secret and token contents remain redacted.
