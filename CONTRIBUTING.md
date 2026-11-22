# Contributing

Thanks for improving doops. Keep changes focused, tested, and free of environment-specific secrets.

## Development

Run the relevant package tests before opening a pull request:

```bash
(cd backend/agent && go test ./...)
(cd backend/gateway && go test ./...)
(cd backend/cli && go test ./...)
```

For Web or dependency changes, use a clean checkout without `node_modules` and the same Node and pnpm versions as [CI](.github/workflows/ci.yml). Run the frozen install before pushing dependency changes, then typecheck and build:

```bash
corepack enable && corepack prepare pnpm@11.7.0 --activate
CI=1 pnpm install --frozen-lockfile
pnpm exec tsc --noEmit
pnpm build
```

If pnpm rejects a newly published package under its release-age policy, wait for the selected version to qualify and rerun the check. Do not exempt the package or substitute an install from an existing dependency directory.

## Security and Configuration

- Do not commit passwords, tokens, kubeconfigs, private keys, production IPs, or internal registry credentials.
- Use placeholders such as `gateway.example.com`, `registry.example.com`, and `<GATEWAY_USER_TOKEN>` in docs and tests.
- Put local runtime config under ignored paths such as `~/.agent/skills/doops/` or environment variables.
- If a secret was committed, rotate it first, then remove it from both the working tree and git history before publishing.
