import json
import subprocess
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]
RELEASE = ROOT / "scripts" / "release-managed-agent.sh"
IMAGE = ROOT / "scripts" / "doops-release-image.sh"
CHART = ROOT / "deploy" / "helm" / "doops-agent" / "templates" / "deployment.yaml"
VALUES = ROOT / "deploy" / "helm" / "doops-agent" / "values.yaml"
SCHEMA = ROOT / "deploy" / "helm" / "doops-agent" / "values.schema.json"
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


def test_agent_chart_requires_an_explicit_proxy_upstream_allowlist():
    deployment = CHART.read_text()
    values = yaml.safe_load(VALUES.read_text())
    schema = json.loads(SCHEMA.read_text())

    assert values["proxy"]["allowedHosts"] == ""
    assert "DOOPS_PROXY_ALLOW_HOSTS" in deployment
    assert ".Values.proxy.allowedHosts" in deployment
    assert schema["properties"]["proxy"] == {
        "type": "object",
        "additionalProperties": False,
        "properties": {"allowedHosts": {"type": "string"}},
    }


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
    assert "--proxy-allowed-hosts" in text
    assert 'values.setdefault("proxy", {})["allowedHosts"] = proxy_allowed_hosts' in text
    assert "normalize_proxy_allowed_hosts" in text
    assert "replacement Agent proxy allowlist does not match live Helm values" in text
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


def test_release_rejects_proxy_urls_before_contacting_a_target():
    result = subprocess.run(
        [
            "bash",
            str(RELEASE),
            "--target",
            "not-a-real-target",
            "--session",
            "proxy-validation",
            "--release",
            "doops-agent",
            "--namespace",
            "doops-system",
            "--proxy-allowed-hosts",
            "https://internal.example.com:8443/path",
            "--image",
            "docker.cnb.cool/l8ai/ai/doops.sh@sha256:" + "a" * 64,
            "--dry-run",
        ],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
    )

    assert result.returncode == 2
    assert "entries must be exact hostnames or IPs" in result.stderr
    assert "TARGETING" not in result.stdout


def test_release_rejects_empty_proxy_entries():
    result = subprocess.run(
        [
            "bash",
            str(RELEASE),
            "--target",
            "not-a-real-target",
            "--session",
            "proxy-validation",
            "--release",
            "doops-agent",
            "--namespace",
            "doops-system",
            "--proxy-allowed-hosts",
            "127.0.0.1,,internal.example.com",
            "--image",
            "docker.cnb.cool/l8ai/ai/doops.sh@sha256:" + "a" * 64,
            "--dry-run",
        ],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
    )

    assert result.returncode == 2
    assert "contains an empty entry" in result.stderr
    assert "TARGETING" not in result.stdout
