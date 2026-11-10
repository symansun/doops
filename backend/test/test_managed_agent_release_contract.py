import json
import os
import subprocess
from pathlib import Path

import pytest
import yaml


ROOT = Path(__file__).resolve().parents[1]
RELEASE = ROOT / "scripts" / "release-managed-agent.sh"
IMAGE = ROOT / "scripts" / "doops-release-image.sh"
CHART = ROOT / "deploy" / "helm" / "doops-agent" / "templates" / "deployment.yaml"
VALUES = ROOT / "deploy" / "helm" / "doops-agent" / "values.yaml"
SCHEMA = ROOT / "deploy" / "helm" / "doops-agent" / "values.schema.json"


@pytest.mark.parametrize("host_access", [None, True, False])
def test_agent_chart_hands_off_without_dropping_the_live_replica(host_access):
    command = [
        "helm", "template", "doops-agent", str(VALUES.parent),
        "--set", "workload.name=doops-agent",
        "--set", "gateway.url=https://gateway.example.test",
        "--set", "gateway.cluster=test-cluster",
        "--set", "gateway.instance=test-agent",
        "--set", "image.repository=registry.example.test/doops-agent",
        "--set", "image.tag=test",
    ]
    if host_access is not None:
        command.extend(["--set", f"hostAccess.enabled={str(host_access).lower()}"])
    result = subprocess.run(command, capture_output=True, text=True, check=True)
    deployment = next(item for item in yaml.safe_load_all(result.stdout) if item)
    spec = deployment["spec"]
    pod = spec["template"]["spec"]
    container = pod["containers"][0]
    assert spec["strategy"] == {
        "type": "RollingUpdate",
        "rollingUpdate": {"maxUnavailable": 0, "maxSurge": 1},
    }
    assert pod["terminationGracePeriodSeconds"] == 30
    assert spec["minReadySeconds"] == 5
    assert pod["hostNetwork"] is False
    assert pod["hostPID"] is (host_access is not False)
    assert "/app/sandbox-entrypoint.sh" in container["command"][-1]
    for name, endpoint in (
        ("startupProbe", "live"), ("readinessProbe", "ready"), ("livenessProbe", "live")
    ):
        probe = container[name]["exec"]["command"][-1]
        assert f"http://127.0.0.1:42222/health/{endpoint}" in probe
        assert ("pgrep" in probe) is (host_access is False)


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
    assert "doops_cmd push" not in text
    assert "doops_cmd exec" not in text
    assert 'upgrade --install "${RELEASE}" "${CHART_DIR}"' in text
    assert 'helm rollback "${RELEASE}" "${previous_revision}"' in text
    assert 'if ! helm rollback "${RELEASE}" "${previous_revision}"' in text
    assert 'if ! verify_final_deployment_state "${workload}" "${current_image}"; then' in text
    assert "Error: Helm rollback command failed" in text
    assert "--dry-run=server" in text
    assert "rollback_complete=true" in text
    assert "candidate_failure_observation" in text
    assert "trap 'exit 130' INT" in text
    assert "trap 'exit 143' TERM" in text
    assert "hostNetwork agents cannot hand off" in text
    assert "candidate chart must disable hostNetwork" in text
    assert "sandbox-entrypoint.sh" in text
    assert "not a normal workload deployment" in text
    assert "ordinary workload releases, use:" in text
    assert "external operator kubeconfig" in text
    assert 'deploy.get("spec")' in text
    assert '"selector", {}).get("matchLabels")' in text
    assert 'values.setdefault("workload", {})["selectorLabels"]' in text
    assert "live Deployment selector does not match template labels" in text
    assert "deploy/helm/doops-agent" in text
    assert "went offline for 30s during handoff" in text
    assert "--values" in text
    assert "--kubeconfig" in text
    assert "--cluster" in text
    assert "--instance" in text
    assert "Gateway target resolves to" in text
    assert "--proxy-allowed-hosts" in text
    assert 'values.setdefault("proxy", {})["allowedHosts"] = proxy_allowed_hosts' in text
    assert "normalize_proxy_allowed_hosts" in text
    assert "replacement Agent proxy allowlist does not match live Helm values" in text
    assert 'container_env.get("DOOPS_PROXY_ALLOW_HOSTS", "")' in text
    assert 'os.environ.get("DOOPS_PROXY_ALLOW_HOSTS", "")' not in text
    assert "--host-alias" in text
    assert 'values["hostAliases"] = entries' in text
    assert "normalize_host_aliases" in text
    assert "replacement Agent host aliases do not match live Helm values" in text
    assert "replacement Agent did not resolve" in text
    assert "pre-release-pods.json" in text
    assert '--cmd hostname' in text
    assert 'kubectl -n "${NAMESPACE}" exec "${replacement_pod}"' in text
    assert "doops -session" in text
    assert "doops capabilities --help" in text
    assert "installed doops CLI does not expose a full release commit" in text
    assert text.index("doops capabilities --help") < text.index(
        'helm_status_output=""'
    )
    assert "lacks the capabilities protocol probe" in text
    assert 'doops -session "${SESSION}-capabilities" capabilities \\' in text
    assert '--target "${TARGET}" --require structuredSSH' in text
    assert "did not advertise structuredSSH" in text
    assert "replacement Agent release commit mismatch" in text
    assert "Kubernetes API is unreachable or rejected the kubeconfig" in text
    assert 'kubectl version --request-timeout=10s' in text
    assert "was not found or is not readable" in text
    assert "only when the Kubernetes API is reachable" in text
    assert "@sha256:" in helper
    assert "docker.cnb.cool/l8ai/ai/doops.sh" in helper


def test_existing_release_requires_an_external_kubeconfig_before_target_contact():
    env = os.environ.copy()
    env.pop("KUBECONFIG", None)
    result = subprocess.run(
        [
            "bash",
            str(RELEASE),
            "--target",
            "not-a-real-target",
            "--session",
            "external-controller",
            "--cluster",
            "doops-test",
            "--instance",
            "node-1",
            "--release",
            "doops-agent",
            "--namespace",
            "doops-system",
            "--image",
            "docker.cnb.cool/l8ai/ai/doops.sh@sha256:" + "a" * 64,
            "--dry-run",
        ],
        cwd=ROOT,
        env=env,
        text=True,
        capture_output=True,
        check=False,
    )

    assert result.returncode == 2
    assert "not a normal workload deployment" in result.stderr
    assert "external operator kubeconfig is required" in result.stderr
    assert "For a Helm-managed Agent with the self-upgrade controller" in result.stderr
    assert "upgrade --target <target> --image <repository>@sha256:<digest>" in result.stderr
    assert "does not require a local kubeconfig" in result.stderr
    assert "because the current Agent cannot replace its own Deployment" not in result.stderr
    assert "TARGETING" not in result.stdout


def test_bootstrap_requires_explicit_kubeconfig_before_running_cluster_tools():
    env = os.environ.copy()
    env.pop("KUBECONFIG", None)
    result = subprocess.run(
        [
            "bash", str(RELEASE),
            "--release", "doops-agent",
            "--namespace", "doops-system",
            "--values", "/not-a-real-values-file",
            "--image", "registry.example/doops@sha256:" + "a" * 64,
            "--dry-run",
        ],
        cwd=ROOT,
        env=env,
        text=True,
        capture_output=True,
        check=False,
    )
    assert result.returncode == 2
    assert "--kubeconfig (or KUBECONFIG) is required" in result.stderr
    assert "Release complete:" not in result.stdout


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


def test_release_rejects_invalid_host_alias_before_contacting_a_target():
    result = subprocess.run(
        [
            "bash",
            str(RELEASE),
            "--target",
            "not-a-real-target",
            "--session",
            "host-alias-validation",
            "--release",
            "doops-agent",
            "--namespace",
            "doops-system",
            "--host-alias",
            "https://demo.example.com=not-an-ip",
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
    assert "--host-alias HOST must be an exact hostname" in result.stderr
    assert "TARGETING" not in result.stdout


def test_release_rejects_conflicting_host_aliases():
    result = subprocess.run(
        [
            "bash",
            str(RELEASE),
            "--target",
            "not-a-real-target",
            "--session",
            "host-alias-validation",
            "--release",
            "doops-agent",
            "--namespace",
            "doops-system",
            "--host-alias",
            "demo.example.com=47.114.103.22",
            "--host-alias",
            "demo.example.com=192.168.150.228",
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
    assert "assigns conflicting IPs to demo.example.com" in result.stderr
    assert "TARGETING" not in result.stdout
