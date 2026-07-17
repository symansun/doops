from pathlib import Path


ROOT = Path(__file__).parents[1]
SKILL = ROOT / "agent" / "skills" / "doops-cicd" / "SKILL.md"


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
