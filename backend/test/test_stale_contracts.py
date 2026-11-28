"""Central scan for retired DoOps contracts.

Retired names live only here and in AGENTS.md. Operator documents describe the current
behavior and do not need "there is no X" sentences to keep these tests green.
"""

import re
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
BACKEND = ROOT / "backend"

# Private production endpoints and developer workstation paths must never leak into the public repo.
# Constructed programmatically so literals do not appear in this test file either.
PRIVATE_PUBLIC_REPO_PATTERNS = (
    ("production_gateway_ip", re.compile(re.escape(".".join(("106", "54", "197", "139"))))),
    ("production_silicon_ip", re.compile(re.escape(".".join(("49", "51", "244", "8"))))),
    ("legacy_public_host_ip", re.compile(re.escape(".".join(("47", "114", "103", "22"))))),
    ("campus_public_ip_1", re.compile(re.escape(".".join(("47", "114", "1", "135"))))),
    ("campus_public_ip_2", re.compile(re.escape(".".join(("114", "55", "91", "187"))))),
    ("campus_public_ip_3", re.compile(re.escape(".".join(("47", "114", "58", "206"))))),
    ("campus_public_ip_4", re.compile(re.escape(".".join(("119", "6", "253", "100"))))),
    ("aliyun_rds_endpoint", re.compile(r"[a-z0-9.-]+\.rds\.aliyuncs\.com\b", re.IGNORECASE)),
    ("tencent_rds_endpoint", re.compile(r"[a-z0-9.-]+\.tencentcdb\.com\b", re.IGNORECASE)),
    ("private_org_slug", re.compile(re.escape("aie" + "dulab"), re.IGNORECASE)),
    ("private_domain_root", re.compile(re.escape(".".join(("aie" + "dulab", "cn"))), re.IGNORECASE)),
    ("private_regcred", re.compile(re.escape("-".join(("aie" + "dulab", "regcred"))), re.IGNORECASE)),
    ("campus_internal_domain", re.compile(re.escape(".".join(("zjcm", "edu", "cn"))), re.IGNORECASE)),
    ("private_cos_bucket", re.compile(re.escape("-".join(("zhiyong", "1253657668"))), re.IGNORECASE)),
    ("private_ssh_alias", re.compile(re.escape("-".join(("sub2api", "49"))), re.IGNORECASE)),
    ("developer_workstation_path", re.compile(re.escape("/" + "Users" + "/" + "ww" + "yz"))),
)

# Internal dev-process ledgers, scratch reviews, and site-specific deployment cutover records
# must never be tracked in the public repository.
FORBIDDEN_TRACKED_PREFIXES = (
    "docs/operations/",
    "docs/reviews/",
    "docs/design/",
    "docs/reports/",
    "docs/archive/",
    "docs/superpowers/",
    "backend/docs/reviews/",
    "backend/docs/reports/",
    "backend/docs/superpowers/",
    "backend/deploy/instances/",
    "backend/deploy/fleet-",
    "DoSecurity/scan-runs/",
)

FORBIDDEN_TRACKED_EXACT = {
    "HELM_GATE_CLEANUP.md",
    "docs/README.md",
}

# Retired deployment roots and tools. None may reappear in code, CI, Charts, or docs.
RETIRED_IDENTIFIERS = (
    "EnvironmentRegistry",
    "EnvironmentDefinition",
    "DeploymentTemplate",
    "deploy/environments.yaml",
    "cicd run",
    "doops cicd",
    "execute-doops-cicd",
    "CICDReport",
    "doops_cicd_result",
    "api_trigger_bootstrap_release_runner",
    "bootstrap-release-runner.sh",
    "upgrade-managed-agent.sh",
    "install-managed-agent.sh",
    "extract-gateway-from-image.sh",
    "doagent_apply_workspace_commit_required",
    "validateWorkspaceCommitBinding",
    "validateBareWorkspaceCommitBinding",
    "image.tag=release-YYYYMMDD",
    "add-image-pull-secret",
    "auto-harness-secret",
)

# Retired operator guidance. Scanned only in documents and prompts; some of these strings
# are legitimate inside code (for example the CLI image builder).
RETIRED_GUIDANCE = (
    "git archive --format=tar.gz HEAD",
    "SCP 传输到构建节点",
    "buildctl --addr",
    "latest,push=true",
    "registry.insecure=true",
    "kubectl -n kube-system set image",
    "kubectl run <node>-doops-deployer",
    "chroot /host /usr/bin/nsenter",
    "再启动新 agent 或触发 DaemonSet 滚动更新",
    "nerdctl ps | grep doops-agent",
    "/dowork/-/git/raw/main/doops-k8s/install.sh",
    "doops upgrade` 只允许 `--dry-run`",
    "preview-only interfaces",
    "临时配置单个 Gateway route、同步当前仓库",
    "同步当前仓库并提交",
    "目标由环境注册表解析",
    "公共 Agent 不接收凭据 payload",
    "凭据 payload 不进入 prompt、Agent",
    "fingerprint/expiry",
    "SSH 只用于首次安装、自恢复 agent",
    "才可通过前台 `doops exec` 执行 SSH 恢复",
    "单机或没有 DaemonSet 的节点，推荐标准安装",
    "生产环境建议升级成 K8s/DaemonSet 或容器模式",
)

SOURCE_SUFFIXES = {".go", ".sh", ".py", ".mjs", ".js", ".ts", ".tsx", ".yaml", ".yml", ".json", ".md", ".tpl"}


def _files(root: Path, suffixes=SOURCE_SUFFIXES):
    for path in sorted(root.rglob("*")):
        if not path.is_file() or path.suffix not in suffixes:
            continue
        parts = set(path.relative_to(ROOT).parts)
        if parts & {"node_modules", ".next", "__pycache__", ".pytest_cache", "bin"}:
            continue
        yield path


def _code_and_contract_files():
    yield from _files(ROOT / ".github" / "workflows", {".yml", ".yaml"})
    for name in ("agent", "gateway", "scripts", "deploy"):
        yield from _files(BACKEND / name)
    yield from _files(ROOT / "skills")
    yield from _files(BACKEND / "docs", {".md"})
    yield BACKEND / "README.md"


def _guidance_files():
    yield ROOT / "skills" / "doops" / "SKILL.md"
    yield BACKEND / "README.md"
    yield ROOT / "README.md"
    yield from _files(BACKEND / "docs", {".md"})
    yield from _files(ROOT / "skills" / "agent", {".md"})


def _hits(paths, needles):
    hits = []
    for path in paths:
        text = path.read_text(encoding="utf-8", errors="replace")
        for needle in needles:
            if needle in text:
                hits.append(f"{path.relative_to(ROOT)}: {needle}")
    return hits


def _public_sanitize_files():
    yield from _files(ROOT / ".github" / "workflows", {".yml", ".yaml"})
    yield ROOT / ".cnb.yml"
    for name in ("agent", "cli", "deploy", "gateway", "scripts"):
        yield from _files(BACKEND / name, SOURCE_SUFFIXES | {".txt"})
    for name in ("app", "components", "lib", "skills"):
        yield from _files(ROOT / name)
    yield from _files(BACKEND / "docs", {".md"})
    yield BACKEND / "README.md"
    yield ROOT / "README.md"
    yield ROOT / "AGENTS.md"
    yield ROOT / "CHANGELOG.md"


def _tracked_repo_files():
    proc = subprocess.run(
        ["git", "ls-files"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode == 0 and proc.stdout.strip():
        return [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    return [str(p.relative_to(ROOT)).replace("\\", "/") for p in _files(ROOT, SOURCE_SUFFIXES | {".txt", ".html"})]


def test_retired_identifiers_do_not_reappear():
    assert _hits(_code_and_contract_files(), RETIRED_IDENTIFIERS) == []


def test_retired_guidance_does_not_reappear():
    assert _hits(_guidance_files(), RETIRED_GUIDANCE) == []


def test_public_repo_does_not_leak_private_deploy_endpoints():
    hits = []
    scanned = 0
    for path in _public_sanitize_files():
        if not path.is_file():
            continue
        scanned += 1
        text = path.read_text(encoding="utf-8", errors="replace")
        for label, pattern in PRIVATE_PUBLIC_REPO_PATTERNS:
            if pattern.search(text):
                hits.append(f"{path.relative_to(ROOT)}: {label}")
    assert scanned > 50, f"unexpectedly small scan surface: {scanned}"
    assert hits == []


def test_public_repo_forbids_internal_process_artifacts():
    violations = []
    for rel in _tracked_repo_files():
        if rel in FORBIDDEN_TRACKED_EXACT or any(rel.startswith(prefix) for prefix in FORBIDDEN_TRACKED_PREFIXES):
            violations.append(rel)
    assert violations == [], f"internal process artifacts must not be tracked: {violations}"
