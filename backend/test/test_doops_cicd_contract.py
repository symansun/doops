from pathlib import Path


ROOT = Path(__file__).parents[1]
SKILL = ROOT / "agent" / "skills" / "doops-cicd" / "SKILL.md"
ACTIVE_CONTRACT_DOCS = (
    ROOT / "skills" / "SKILL.md",
    ROOT / "docs" / "SEMANTIC_CICD.md",
    ROOT / "docs" / "BUILD_DEPLOY_SOP.md",
    ROOT / "docs" / "AGENT_NATIVE_CICD.md",
    ROOT / "docs" / "design" / "AI_Ops_System_Design.md",
    ROOT.parent / "docs" / "credential-plan-convergence.md",
)


def test_cicd_skill_uses_gateway_bound_v3_single_service_contract():
    text = SKILL.read_text()
    assert "deploy/environments.yaml" in text
    assert "`environment`" in text
    assert "`service`" in text
    assert "Gateway" in text
    assert "CredentialRun" in text
    assert "doops.reconciliation-context/v2" in text
    assert "遍历其他服务" in text
    assert "fallback" in text
    for removed in (
        "EnvironmentDefinition",
        "DeploymentTemplate",
        "SemanticRelease",
        "ServiceRelease",
        "DeploymentRun",
        "doops_plan",
    ):
        assert removed not in text


def test_active_cicd_docs_only_describe_gateway_bound_v3_registry():
    text = "\n".join(path.read_text() for path in ACTIVE_CONTRACT_DOCS)

    for required in (
        "deploy/environments.yaml",
        "doops.sh/v3",
        "EnvironmentRegistry",
        "environment",
        "service",
        "doops.sh/credential-plan/v2",
        "doops.reconciliation-context/v2",
    ):
        assert required in text

    for removed in (
        "v2 DeploymentTemplate |",
        "uses `DeploymentTemplate`",
        "deploy/workflows/oilan-agent-bootstrap",
        "configurationSource",
        "--set releaseId",
        "DeploymentTemplate -> doops push",
    ):
        assert removed not in text
