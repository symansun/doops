from pathlib import Path


ROOT = Path(__file__).parents[1]
REPO_ROOT = ROOT.parent
SKILL = ROOT / "agent" / "skills" / "doops-cicd" / "SKILL.md"
SYSTEM_PROMPT = ROOT / "agent" / "skills" / "system_prompt.md"
DOCKERFILE = ROOT / "Dockerfile"
CNB_PIPELINE = REPO_ROOT / ".cnb.yml"

RETIRED_RELEASE_MARKERS = (
    "doops.sh/v2",
    "credential-plan/v2",
    "reconciliation-context/v2",
    "EnvironmentRegistry",
    "EnvironmentDefinition",
    "DeploymentTemplate",
    "DEPLOYMENT_TEMPLATE",
    "DeploymentRun",
    "CredentialRun",
    "doops_credential_plan",
    "/v1/credentials/prepare",
    "doops cicd run",
    "deploy/environments.yaml",
    "环境注册表",
)


def test_cicd_skill_uses_natural_language_v3_contract():
    text = SKILL.read_text()
    assert "自然语言发布任务" in text
    assert "target" in text
    assert "服务范围" in text
    assert "mutation 授权" in text
    assert "零工具调用必须失败" in text
    assert "fallback" in text
    for forbidden in RETIRED_RELEASE_MARKERS:
        assert forbidden not in text


def test_system_prompt_matches_natural_language_v3_contract():
    text = SYSTEM_PROMPT.read_text()
    assert "自然语言任务" in text
    assert "实际调用" in text
    assert "零个已完成工具调用必须失败" in text
    for forbidden in RETIRED_RELEASE_MARKERS + ("configurationSource", "不得激活旧"):
        assert forbidden not in text


def test_retired_release_protocol_is_absent_from_active_sources():
    roots = (
        ROOT / "agent",
        ROOT / "docs",
        ROOT / "skills",
        ROOT / "deploy",
        REPO_ROOT / "components",
        REPO_ROOT / "docs",
        REPO_ROOT / "deploy",
    )
    suffixes = {".go", ".md", ".py", ".sh", ".tsx", ".ts", ".yaml", ".yml"}
    violations = []
    for root in roots:
        if not root.exists():
            continue
        for path in root.rglob("*"):
            if not path.is_file() or path.suffix not in suffixes:
                continue
            if "bin" in path.parts or path == Path(__file__):
                continue
            text = path.read_text(errors="ignore")
            for marker in RETIRED_RELEASE_MARKERS:
                if marker in text:
                    violations.append(f"{path.relative_to(REPO_ROOT)}: {marker}")
    assert not violations, "\n".join(violations)


def test_ci_image_contains_agent_and_gateway_from_the_same_revision():
    dockerfile = DOCKERFILE.read_text()
    pipeline = CNB_PIPELINE.read_text()

    assert "go build -o /app/doops-agent ./cmd/agent" in dockerfile
    assert "go build -o /app/doops-gateway ." in dockerfile
    assert "COPY --from=builder /app/doops-gateway /app/doops-gateway" in dockerfile
    assert "/app/doops-gateway -help" in dockerfile
    assert "docker run --rm --entrypoint /app/doops-gateway" in pipeline
