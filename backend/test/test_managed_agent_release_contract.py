from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
RELEASE = ROOT / "scripts" / "release-managed-agent.sh"
IMAGE = ROOT / "scripts" / "doops-release-image.sh"
CHART = ROOT / "deploy" / "helm" / "doops-agent" / "templates" / "deployment.yaml"
SOP = ROOT / "docs" / "BUILD_DEPLOY_SOP.md"
GUIDE = ROOT / "docs" / "CLUSTER_INSTALL_GUIDE.md"
USAGE = ROOT / "docs" / "DOOPS_USAGE_GUIDE.md"
SKILL = ROOT / "skills" / "SKILL.md"


def test_agent_chart_hands_off_without_dropping_the_live_replica():
    text = CHART.read_text()
    assert "maxUnavailable: 0" in text
    assert "maxSurge: 1" in text
    assert "terminationGracePeriodSeconds: 30" in text
    assert "minReadySeconds: 5" in text
    assert "hostNetwork:" not in text
    assert "hostPID:" not in text
    assert "/app/sandbox-entrypoint.sh" in text


def test_single_managed_agent_release_script():
    text = RELEASE.read_text()
    helper = IMAGE.read_text()

    assert not (ROOT / "scripts" / "upgrade-managed-agent.sh").exists()
    assert not (ROOT / "scripts" / "install-managed-agent.sh").exists()
    assert "--wait=false" in text
    assert "--reuse-values" not in text
    assert "kubectl set image" not in text
    assert "doops exec --bg" not in text
    assert "helm upgrade --install" in text
    assert "image pull failed; refuse to roll" in text
    assert "hostNetwork agents cannot hand off" in text
    assert "sandbox-entrypoint.sh" in text
    assert "deploy/helm/doops-agent" in text
    assert "went offline for 30s during handoff" in text
    assert "--values" in text
    assert "doops -session" in text
    assert "@sha256:" in helper
    assert "docker.cnb.cool/l8ai/ai/doops.sh" in helper


def test_docs_point_agent_release_at_one_script():
    sop = SOP.read_text()
    guide = GUIDE.read_text()
    usage = USAGE.read_text()
    skill = SKILL.read_text()

    for text in (sop, guide, usage, skill):
        assert "release-managed-agent.sh" in text
        assert "upgrade-managed-agent.sh" not in text
        assert "install-managed-agent.sh" not in text
    assert "maxSurge=1" in sop
    assert "maxUnavailable=0" in sop
    assert "sandbox-entrypoint.sh" in usage
