# Agent-Native CI/CD

Agent-native CI/CD is the `doops ask --allow-mutate` path documented in
`SEMANTIC_CICD.md`.

- Input is a complete natural-language release task.
- Authorization is the explicit CLI mutation flag plus Gateway exec permission.
- Execution uses runtime tools in a temporary working directory.
- Success requires completed tool activity and online readback.
- YAML workflow roots, workspace snapshots, immutable workspace commits and
  generated orchestration wrappers are not supported release inputs.
