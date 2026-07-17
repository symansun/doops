from pathlib import Path


ROOT = Path(__file__).parents[1]
SKILL = ROOT / "agent" / "skills" / "doops-cicd" / "SKILL.md"


def test_cicd_skill_uses_natural_language_without_workflow_files():
    text = SKILL.read_text()
    assert "自然语言" in text
    assert "target" in text
    assert "镜像" in text
    assert "验收条件" in text
    assert "deploy/environments.yaml" not in text
    assert "deploy/deploy.yaml" not in text
    assert "cicd run" not in text
    assert "credential-plan" not in text
