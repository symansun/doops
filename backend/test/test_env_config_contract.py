"""Contract guard for DoOps configuration templates and environment variables.

Ensures:
1. Every production environment variable read in Gateway, Agent, Web Console, or CLI
   source code is documented in backend/docs/CONFIGURATION.md.
2. Every .env.example template has valid dotenv/EnvironmentFile syntax, references only
   documented environment variables, and leaks zero private endpoints or real secrets.
3. The systemd service template, .gitignore rules, Helm values.yaml comments, and
   documentation indexes stay synchronized with backend/docs/CONFIGURATION.md.
"""

from pathlib import Path
import re

from test_stale_contracts import PRIVATE_PUBLIC_REPO_PATTERNS


ROOT = Path(__file__).resolve().parents[2]
CONFIG_DOC = ROOT / "backend" / "docs" / "CONFIGURATION.md"

ENV_PREFIXES = (
    "DOOPS_",
    "DO_AGENT_",
    "DOSQL_",
    "DOWEB_",
    "NEXT_PUBLIC_DOOPS_",
)

DYNAMIC_ENV_FAMILIES = (
    "DOOPS_INPUT_",
    "DOOPS_PLUGIN_",
    "DOOPS_CRED_FILE_",
    "DOOPS_UPGRADE_",
)

# Patterns that extract real runtime environment variable reads/declarations from source.
SOURCE_ENV_PATTERNS = (
    re.compile(r'os\.(?:Getenv|LookupEnv)\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'positiveEnvInt64\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'positiveEnvInt\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'envDuration(?:Compat)?\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'parseEnvBoolFlag\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'os\.environ(?:\.get)?[\(\[]\s*["\']([A-Z][A-Z0-9_]+)["\']'),
    re.compile(r'\bEnv\s*:\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(
        r'\b[A-Za-z0-9_]*[Ee]nv[A-Za-z0-9_]*\s*=\s*"((?:DOOPS|DO_AGENT|DOSQL|DOWEB)_[A-Z0-9_]+)"'
    ),
    re.compile(r'process\.env\.([A-Z][A-Z0-9_]+)'),
    re.compile(r'process\.env\[\s*["\']([A-Z][A-Z0-9_]+)["\']\s*\]'),
    re.compile(
        r'\$\{((?:DOOPS|DO_AGENT|DOSQL|DOWEB)_[A-Z0-9_]+)[:?-]'
    ),
)

SOURCE_DIRS = (
    "backend/agent",
    "backend/gateway",
    "backend/cli",
    "app",
    "lib",
    "components",
)

SOURCE_SUFFIXES = {".go", ".py", ".ts", ".tsx", ".js", ".mjs", ".sh"}
IGNORED_PATH_PARTS = {"node_modules", ".next", "bin", "__pycache__", ".pytest_cache"}

ENV_TEMPLATES = {
    ROOT / ".env.example": {
        "DOOPS_CONSOLE_ALLOWED_GATEWAYS",
        "DOOPS_CONSOLE_ORIGIN",
        "DOOPS_CONSOLE_TRUSTED_PROXY_HOPS",
        "DOOPS_CONSOLE_FORWARD_SECRET",
        "NEXT_PUBLIC_DOOPS_DEMO",
    },
    ROOT / "backend" / "deploy" / "config" / "doops-gateway.env.example": {
        "DOOPS_GATEWAY_SECRET_KEY",
        "DOOPS_GATEWAY_STORE",
        "DOOPS_GATEWAY_MYSQL_DSN",
        "DOOPS_GATEWAY_DATA_DIR",
        "DOOPS_AUDIT_STORAGE",
        "DOOPS_REQUIRE_AGENT_ENROLLMENT",
        "DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY",
        "DOOPS_CONSOLE_FORWARD_SECRET",
        "DOOPS_ALLOWED_WS_ORIGINS",
    },
    ROOT / "backend" / "deploy" / "config" / "doops-agent.env.example": {
        "DOOPS_GATEWAY_URL",
        "DOOPS_GATEWAY_CLUSTER",
        "DOOPS_GATEWAY_INSTANCE",
        "DOOPS_AGENT_ENROLL_TOKEN",
        "DOOPS_PROXY_ALLOW_HOSTS",
        "DOOPS_COMMAND_POLICY_FILE",
        "DOOPS_COMMAND_DENY_RULES",
        "DOOPS_DOSQL_STATE_DIR",
        "DOOPS_TOOL_TIMEOUT",
    },
}

SYSTEMD_TEMPLATE = ROOT / "backend" / "deploy" / "systemd" / "doops-gateway.service.example"

REAL_SECRET_PATTERNS = (
    ("real_enrollment_token", re.compile(r"dga_aen_aen_[0-9a-f]{24}_[0-9a-f]{64}")),
    ("real_gateway_token", re.compile(r"\bdga_[0-9a-f]{32,}\b")),
    ("real_hex_secret_key", re.compile(r"SECRET_KEY=[0-9a-fA-F]{64}\b")),
)


def _discover_production_env_vars() -> dict[str, set[str]]:
    discovered: dict[str, set[str]] = {}
    for rel_dir in SOURCE_DIRS:
        base = ROOT / rel_dir
        if not base.exists():
            continue
        for path in sorted(base.rglob("*")):
            if not path.is_file() or path.suffix not in SOURCE_SUFFIXES:
                continue
            name = path.name
            if name.endswith("_test.go") or ".test." in name or ".browser-test." in name:
                continue
            rel = path.relative_to(ROOT)
            if set(rel.parts) & IGNORED_PATH_PARTS:
                continue
            text = path.read_text(encoding="utf-8", errors="replace")
            for pattern in SOURCE_ENV_PATTERNS:
                for match in pattern.findall(text):
                    if match.startswith(ENV_PREFIXES):
                        discovered.setdefault(match, set()).add(str(rel))
    return discovered


def _parse_env_template(path: Path) -> tuple[set[str], list[str]]:
    keys: set[str] = set()
    errors: list[str] = []
    text = path.read_text(encoding="utf-8")
    assert text.strip(), f"{path.relative_to(ROOT)} must not be empty"

    for lineno, raw in enumerate(text.splitlines(), start=1):
        stripped = raw.strip()
        if not stripped:
            continue
        if stripped.startswith("#"):
            # Inspect commented KEY=VALUE example lines.
            candidate = stripped.lstrip("#").strip()
            m = re.match(r"^([A-Z][A-Z0-9_]*)=(.*)$", candidate)
            if m:
                key, value = m.group(1), m.group(2)
                if key.startswith(ENV_PREFIXES):
                    keys.add(key)
                if _has_unclosed_quote(value):
                    errors.append(f"{path.name}:{lineno}: unclosed quote in commented assignment")
            continue

        m = re.match(r"^([A-Z][A-Z0-9_]*)=(.*)$", stripped)
        if not m:
            errors.append(f"{path.name}:{lineno}: invalid active env line: {raw!r}")
            continue
        key, value = m.group(1), m.group(2)
        if raw != stripped or " =" in raw.split("=", 1)[0] + "=":
            errors.append(f"{path.name}:{lineno}: unexpected whitespace around KEY=VALUE: {raw!r}")
        if _has_unclosed_quote(value):
            errors.append(f"{path.name}:{lineno}: unclosed quote in active assignment")
        keys.add(key)
    return keys, errors


def _has_unclosed_quote(value: str) -> bool:
    v = value.strip()
    if len(v) == 0:
        return False
    if v[0] in ('"', "'"):
        quote = v[0]
        return len(v) < 2 or not v.endswith(quote)
    return False


def _scan_private_and_secret_leaks(path: Path) -> list[str]:
    text = path.read_text(encoding="utf-8")
    hits = []
    for label, pattern in (*PRIVATE_PUBLIC_REPO_PATTERNS, *REAL_SECRET_PATTERNS):
        if pattern.search(text):
            hits.append(f"{path.relative_to(ROOT)}: matched forbidden pattern {label}")
    return hits


def test_all_production_env_vars_are_documented_in_configuration_md():
    assert CONFIG_DOC.is_file(), "backend/docs/CONFIGURATION.md must exist"
    doc = CONFIG_DOC.read_text(encoding="utf-8")
    discovered = _discover_production_env_vars()
    assert len(discovered) >= 75, (
        f"expected at least 75 production env vars across codebase, got {len(discovered)}"
    )

    missing: list[str] = []
    for var in sorted(discovered):
        if f"`{var}`" in doc:
            continue
        family = next((prefix for prefix in DYNAMIC_ENV_FAMILIES if var.startswith(prefix)), None)
        if family and f"`{family}*`" in doc:
            continue
        files = ", ".join(sorted(discovered[var])[:3])
        missing.append(f"{var} (read in {files})")

    assert missing == [], (
        "Production environment variables missing from backend/docs/CONFIGURATION.md:\n"
        + "\n".join(f"  - {item}" for item in missing)
    )


def test_env_example_templates_syntax_coverage_and_hygiene():
    doc = CONFIG_DOC.read_text(encoding="utf-8")
    for template_path, required_keys in ENV_TEMPLATES.items():
        assert template_path.is_file(), f"missing template {template_path.relative_to(ROOT)}"
        keys, syntax_errors = _parse_env_template(template_path)
        assert syntax_errors == [], f"syntax errors in {template_path.name}: {syntax_errors}"

        missing_required = sorted(required_keys - keys)
        assert missing_required == [], (
            f"{template_path.relative_to(ROOT)} missing required keys: {missing_required}"
        )

        undocumented = sorted(
            key
            for key in keys
            if key.startswith(ENV_PREFIXES) and f"`{key}`" not in doc
        )
        assert undocumented == [], (
            f"{template_path.relative_to(ROOT)} references undocumented env vars: {undocumented}"
        )

        leaks = _scan_private_and_secret_leaks(template_path)
        assert leaks == [], f"forbidden secrets/endpoints in template: {leaks}"


def test_systemd_template_matches_gateway_contract():
    assert SYSTEMD_TEMPLATE.is_file(), "missing systemd service template"
    text = SYSTEMD_TEMPLATE.read_text(encoding="utf-8")
    for required in (
        "[Unit]",
        "[Service]",
        "[Install]",
        "EnvironmentFile=/etc/doops-gateway.env",
        "ExecStart=/usr/local/bin/doops-gateway serve",
        "KillSignal=SIGTERM",
        "TimeoutStopSec=150",
    ):
        assert required in text, f"{SYSTEMD_TEMPLATE.name} missing required directive {required!r}"
    assert _scan_private_and_secret_leaks(SYSTEMD_TEMPLATE) == []


def test_gitignore_protects_local_env_files_while_tracking_examples():
    gitignore = (ROOT / ".gitignore").read_text(encoding="utf-8").splitlines()
    entries = {line.strip() for line in gitignore if line.strip() and not line.strip().startswith("#")}
    for required in (
        ".env",
        ".env.local",
        ".env.*.local",
        "!.env.example",
        "!*.env.example",
        "!backend/runtime-versions.env",
    ):
        assert required in entries, f".gitignore missing required rule {required!r}"


def test_configuration_doc_registered_across_indexes_and_helm_values():
    agents_md = (ROOT / "AGENTS.md").read_text(encoding="utf-8")
    assert "`backend/docs/CONFIGURATION.md`" in agents_md

    docs_readme = (ROOT / "backend" / "docs" / "README.md").read_text(encoding="utf-8")
    assert "(CONFIGURATION.md)" in docs_readme

    root_readme = (ROOT / "README.md").read_text(encoding="utf-8")
    assert "(backend/docs/CONFIGURATION.md)" in root_readme
    for template_rel in (
        "backend/deploy/config/doops-gateway.env.example",
        "backend/deploy/systemd/doops-gateway.service.example",
        "backend/deploy/config/doops-agent.env.example",
        ".env.example",
    ):
        assert template_rel in root_readme, f"README.md missing reference to {template_rel}"

    catalog = (ROOT / "lib" / "docs-catalog.ts").read_text(encoding="utf-8")
    assert 'slug: "configuration"' in catalog
    assert 'sourcePath: "CONFIGURATION.md"' in catalog

    helm_values = (
        ROOT / "backend" / "deploy" / "helm" / "doops-agent" / "values.yaml"
    ).read_text(encoding="utf-8")
    for expected in (
        "backend/docs/CONFIGURATION.md",
        "DOOPS_GATEWAY_URL",
        "DOOPS_AGENT_ENROLL_TOKEN",
        "DOOPS_PROXY_ALLOW_HOSTS",
        "DOOPS_COMMAND_DENY_RULES",
        "DOOPS_TOOL_TIMEOUT",
        "DOOPS_DOSQL_STATE_DIR",
        "DOOPS_KUBECONFIG_SOURCE",
    ):
        assert expected in helm_values, f"Helm values.yaml missing comment reference to {expected}"
