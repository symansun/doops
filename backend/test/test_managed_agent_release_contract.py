from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
UPGRADE = ROOT / "scripts" / "upgrade-managed-agent.sh"
INSTALL = ROOT / "scripts" / "install-managed-agent.sh"
IMAGE = ROOT / "scripts" / "doops-release-image.sh"
SOP = ROOT / "docs" / "BUILD_DEPLOY_SOP.md"
GUIDE = ROOT / "docs" / "CLUSTER_INSTALL_GUIDE.md"
USAGE = ROOT / "docs" / "DOOPS_USAGE_GUIDE.md"
SKILL = ROOT / "skills" / "SKILL.md"


def test_managed_agent_upgrade_is_laptop_helm_with_wait_false():
    text = UPGRADE.read_text()
    helper = IMAGE.read_text()

    assert "--wait=false" in text
    assert "--reuse-values" not in text
    assert "kubectl set image" not in text
    assert "doops exec --bg" not in text
    assert "helm upgrade --install" in text
    assert "nerdctl -n k8s.io pull" in text
    assert "docker pull" in text
    assert "sandbox-entrypoint.sh" in text
    assert "refusing to replace a custom entrypoint" in text
    assert "deploy/helm/doops-agent" in text
    assert "rsync -a" in text
    assert "doops -session" in text
    assert "--version" in text
    assert "@sha256:" in helper
    assert "docker.cnb.cool/l8ai/ai/doops.sh" in helper
    assert "release-runner" not in text.lower() or "forbidden" in text


def test_managed_agent_install_uses_local_helm_and_complete_values():
    text = INSTALL.read_text()

    assert "--create-namespace" in text
    assert "--wait" in text
    assert "--wait=false" not in text
    assert "--reuse-values" not in text
    assert "doops exec" not in text
    assert "--values" in text
    assert "does not create Secrets" in text
    assert "--set-string \"image.repository=" in text
    assert "--set-string \"image.digest=" in text


def test_docs_point_agent_one_click_at_the_managed_scripts():
    sop = SOP.read_text()
    guide = GUIDE.read_text()
    usage = USAGE.read_text()
    skill = SKILL.read_text()

    assert "upgrade-managed-agent.sh" in sop
    assert "install-managed-agent.sh" in sop
    assert "install-managed-agent.sh" in guide
    assert "upgrade-managed-agent.sh" in usage
    assert "upgrade-managed-agent.sh" in skill
    assert "sandbox-entrypoint.sh" in usage
