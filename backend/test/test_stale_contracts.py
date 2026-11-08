"""Central scan for retired DoOps contracts.

Retired names live only here and in AGENTS.md. Operator documents describe the current
behavior and do not need "there is no X" sentences to keep these tests green.
"""

from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
BACKEND = ROOT / "backend"

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
    yield ROOT / ".cnb.yml"
    for name in ("agent", "gateway", "scripts", "deploy", "skills"):
        yield from _files(BACKEND / name)
    yield from _files(BACKEND / "docs", {".md"})
    yield BACKEND / "README.md"


def _guidance_files():
    yield BACKEND / "skills" / "SKILL.md"
    yield BACKEND / "README.md"
    yield ROOT / "README.md"
    yield from _files(BACKEND / "docs", {".md"})
    yield from _files(BACKEND / "agent" / "skills", {".md"})


def _hits(paths, needles):
    hits = []
    for path in paths:
        text = path.read_text(encoding="utf-8", errors="replace")
        for needle in needles:
            if needle in text:
                hits.append(f"{path.relative_to(ROOT)}: {needle}")
    return hits


def test_retired_identifiers_do_not_reappear():
    assert _hits(_code_and_contract_files(), RETIRED_IDENTIFIERS) == []


def test_retired_guidance_does_not_reappear():
    assert _hits(_guidance_files(), RETIRED_GUIDANCE) == []
