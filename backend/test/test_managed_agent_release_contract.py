import json
import os
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
    assert "hostNetwork: false" in text
    assert "hostPID: false" in text
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


def test_docs_point_agent_release_at_one_script():
    sop = SOP.read_text()
    guide = GUIDE.read_text()
    usage = USAGE.read_text()
    skill = SKILL.read_text()

    for text in (sop, guide, usage, skill):
        assert "release-managed-agent.sh" in text
        assert "upgrade-managed-agent.sh" not in text
        assert "install-managed-agent.sh" not in text
        assert "/dowork/-/git/raw/main/doops-k8s/install.sh" not in text
    assert "maxSurge=1" in sop
    assert "maxUnavailable=0" in sop
    assert "sandbox-entrypoint.sh" in usage


def test_release_guidance_does_not_route_managed_or_normal_deployments_to_local_kubeconfig():
    skill = SKILL.read_text()
    model_standards = (ROOT / "docs" / "MODEL_STANDARDS.md").read_text()
    tunnel = (ROOT / "docs" / "DOOPS_GATEWAY_TUNNEL.md").read_text()
    environment = (ROOT / "docs" / "environments" / "dev-114.md").read_text()

    authoritative_docs = (skill, model_standards, tunnel, environment)
    for text in authoritative_docs:
        normalized = " ".join(text.split())
        assert "ask --" in normalized
        assert "--apply" in normalized
        assert "upgrade" in normalized
        assert "doops upgrade` 只允许 `--dry-run`" not in normalized
        assert "preview-only interfaces" not in normalized

    normalized_model_standards = " ".join(model_standards.split()).lower()
    normalized_environment = " ".join(environment.split()).lower()
    assert "ordinary workload deployments" in normalized_model_standards
    assert "operator machine does not need a kubeconfig" in normalized_model_standards
    assert "operator machine does not need a kubeconfig" in normalized_environment
    assert "only if this target is being adopted" in normalized_environment


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


def test_deployment_routes_are_explicit_in_runtime_and_authoritative_docs():
    for path in (
        ROOT / "agent" / "skills" / "system_prompt.md",
        ROOT / "agent" / "skills" / "k8s" / "SKILL.md",
        SKILL,
        SOP,
        USAGE,
    ):
        text = path.read_text()
        assert "upgrade" in text, path
        assert "kubeconfig" in text, path
        assert "Job" in text, path
    for path in (SOP, ROOT / "docs" / "NATURAL_LANGUAGE_DEPLOYMENT.md"):
        text = path.read_text()
        assert "临时配置单个 Gateway route、同步当前仓库" not in text, path
        assert "同步当前仓库并提交" not in text, path
    design = (ROOT / "docs" / "design" / "AI_Ops_System_Design.md").read_text()
    assert "锁内校验 `.doops-ready`" not in design


def test_deployment_guidance_does_not_reintroduce_manual_agent_replacement():
    sop = SOP.read_text()
    readme = (ROOT / "README.md").read_text()
    for retired in (
        "kubectl run <node>-doops-deployer",
        "chroot /host /usr/bin/nsenter",
        "再启动新 agent 或触发 DaemonSet 滚动更新",
        "nerdctl ps | grep doops-agent",
    ):
        assert retired not in sop
    assert "单机或没有 DaemonSet 的节点，推荐标准安装" not in readme
    assert "生产环境建议升级成 K8s/DaemonSet 或容器模式" not in readme


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
            "https://jm.aiedulab.cn=not-an-ip",
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
            "jm.aiedulab.cn=47.114.103.22",
            "--host-alias",
            "jm.aiedulab.cn=192.168.150.228",
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
    assert "assigns conflicting IPs to jm.aiedulab.cn" in result.stderr
    assert "TARGETING" not in result.stdout
