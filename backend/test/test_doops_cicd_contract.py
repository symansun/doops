from pathlib import Path


ROOT = Path(__file__).parents[1]
SKILL = ROOT / "agent" / "skills" / "doops-cicd" / "SKILL.md"


def test_cicd_skill_uses_natural_language_v3_contract():
    text = SKILL.read_text()
    assert "自然语言发布任务" in text
    assert "target" in text
    assert "服务范围" in text
    assert "镜像版本或不可变 digest" in text
    assert "变更原因" in text
    assert "验收条件" in text
    assert "mutation" in text
    for removed in (
        "doops.sh/v2",
        "EnvironmentRegistry",
        "EnvironmentDefinition",
        "DeploymentTemplate",
        "SemanticRelease",
        "ServiceRelease",
        "DeploymentRun",
        "doops_plan",
        "credential-plan",
        "deploy/environments.yaml",
        "deploy/deploy.yaml",
    ):
        assert removed not in text
