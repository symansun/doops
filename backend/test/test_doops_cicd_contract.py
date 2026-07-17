from pathlib import Path


ROOT = Path(__file__).parents[1]
SKILL = ROOT / "agent" / "skills" / "doops-cicd" / "SKILL.md"
SYSTEM_PROMPT = ROOT / "agent" / "skills" / "system_prompt.md"


def test_cicd_skill_uses_natural_language_v3_contract():
    text = SKILL.read_text()
    assert "自然语言发布任务" in text
    assert "target" in text
    assert "服务范围" in text
    assert "mutation 授权" in text
    assert "workspace snapshot" in text
    assert "零工具调用必须失败" in text
    assert "fallback" in text
    for forbidden in (
        "deploy/environments.yaml",
        "EnvironmentDefinition",
        "DeploymentTemplate",
        "SemanticRelease",
        "ServiceRelease",
        "DeploymentRun",
        "doops_plan",
        "CredentialRun",
        "doops.reconciliation-context/v2",
    ):
        assert forbidden not in text


def test_system_prompt_matches_natural_language_v3_contract():
    text = SYSTEM_PROMPT.read_text()
    assert "自然语言任务" in text
    assert "实际调用" in text
    assert "零个已完成工具调用必须失败" in text
    assert "workspace snapshot" in text
    for forbidden in (
        "EnvironmentRegistry",
        "configurationSource",
        "不得激活旧",
        "声明式发布 YAML",
    ):
        assert forbidden not in text
