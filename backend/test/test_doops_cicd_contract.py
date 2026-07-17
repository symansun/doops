from pathlib import Path


ROOT = Path(__file__).parents[1]
SKILL = ROOT / "agent" / "skills" / "doops-cicd" / "SKILL.md"


def test_cicd_skill_only_uses_v3_registry_and_single_service():
    text = SKILL.read_text()
    assert "deploy/deploy.yaml" in text
    assert "deploy/environments.yaml" in text
    assert "environment" in text
    assert "service" in text
    assert "不得遍历其他服务" in text
    for removed in (
        "doops.sh/v2",
        "EnvironmentDefinition",
        "DeploymentTemplate",
        "SemanticRelease",
        "ServiceRelease",
        "DeploymentRun",
        "doops_plan",
        "credential-plan",
    ):
        assert removed not in text
