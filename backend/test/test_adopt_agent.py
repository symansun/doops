import importlib.util
import json
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[1]
MODULE = ROOT / "agent" / "adopt_agent.py"
CHART = ROOT / "deploy" / "helm" / "doops-agent"
DIGEST = "sha256:" + "a" * 64
IMAGE = "ghcr.io/symansun/doops-agent@" + DIGEST
SETTINGS_CANARY = '{"apiKey":"synthetic-settings-canary"}'


_MODULE = None
CONTROLLER_DIGEST = "sha256:" + "c" * 64


def load():
    # One module instance, so AdoptError raised by fakes is the class the script catches.
    global _MODULE
    if _MODULE is None:
        spec = importlib.util.spec_from_file_location("adopt_agent", MODULE)
        _MODULE = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(_MODULE)
    return _MODULE


def jm_deployment(**overrides):
    """Shape of the live gw-jm unmanaged Agent Deployment."""
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": "doops-agent", "namespace": "doops-system", "uid": "u1",
                     "resourceVersion": "9", "labels": {"app": "doops-agent"}},
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": {"app": "doops-agent"}},
            "template": {
                "metadata": {"labels": {"app": "doops-agent"}},
                "spec": {
                    "serviceAccountName": "doops-agent",
                    "containers": [{
                        "name": "doops-agent",
                        "image": "ghcr.io/symansun/doops-agent@sha256:" + "2" * 64,
                        "env": [
                            {"name": "DOOPS_GATEWAY_URL", "value": "https://gateway.example.com"},
                            {"name": "DOOPS_GATEWAY_CLUSTER", "value": "doops-jm"},
                            {"name": "DOOPS_GATEWAY_INSTANCE", "value": "jm-228"},
                            {"name": "KUBECONFIG", "value": "/tmp/doops-kubeconfig"},
                            {"name": "DO_AGENT_MODEL_ROUTING_POLICY", "value": "single-model"},
                        ],
                        "volumeMounts": [
                            {"name": "entrypoint", "mountPath": "/scripts"},
                            {"name": "doagent-config", "mountPath": "/opt/doagent_config"},
                        ],
                    }],
                    "volumes": [
                        {"name": "entrypoint", "configMap": {"name": "doops-entrypoint"}},
                        {"name": "doagent-config", "configMap": {"name": "doops-agent-settings"}},
                    ],
                },
            },
        },
    }
    for path, value in overrides.items():
        target = deployment
        keys = path.split(".")
        for key in keys[:-1]:
            target = target[key]
        target[keys[-1]] = value
    return deployment


class FakeCluster:
    def __init__(self, deployment=None, releases=(), fail=None):
        self.deployment = deployment or jm_deployment()
        self.releases = list(releases)
        self.fail = fail or {}
        self.calls = []
        self.applied = []

    def __call__(self, args, stdin=None, timeout=120):
        self.calls.append(args)
        text = " ".join(args)
        for marker, message in self.fail.items():
            if marker in text:
                raise load().AdoptError(message)
        if args[:2] == ["helm", "list"]:
            return json.dumps([{"name": r} for r in self.releases])
        if args[0] == "helm":
            return ""
        if "get" in args and "deployment" in args:
            return json.dumps(self.deployment)
        if "get" in args and "configmap" in args and "doops-agent-settings" in args:
            return json.dumps({"data": {"settings.json": SETTINGS_CANARY}})
        if "get" in args and "configmap" in args:
            return json.dumps({"data": {"deployment.json": json.dumps(jm_deployment())}})
        if "get" in args and "secrets" in args:
            return json.dumps({"items": [{"metadata": {"name": "sh.helm.release.v1.doops-agent.v1"}}]})
        if "get" in args and "pod" in args:
            return json.dumps({"status": {"containerStatuses": [
                {"name": "controller", "imageID": "registry.example/doops@" + CONTROLLER_DIGEST}]}})
        if "apply" in args or "replace" in args:
            self.applied.append(json.loads(stdin))
        return ""

    def index(self, needle):
        return next(i for i, call in enumerate(self.calls) if needle in " ".join(call))

    def mutations(self):
        return [c for c in self.calls if c[0] == "helm" and c[1] == "upgrade"
                or any(v in c for v in ("apply", "label", "annotate", "replace", "delete"))]


def adopt(module, cluster, **kwargs):
    module.run = cluster
    params = dict(namespace="doops-system", workload="doops-agent", release="doops-agent",
                  cluster="doops-jm", instance="jm-228", image=IMAGE,
                  verifier=lambda *a, **k: "new-pod-uid")
    params.update(kwargs)
    return module.adopt(**params)


@pytest.mark.parametrize("name,cluster,error", [
    ("existing release", FakeCluster(releases=["doops-agent"]), "already exists"),
    ("two replicas", FakeCluster(jm_deployment(**{"spec.replicas": 2})), "exactly one replica"),
    ("default service account",
     FakeCluster(jm_deployment(**{"spec.template.spec.serviceAccountName": "default"})), "dedicated ServiceAccount"),
    ("selector expressions",
     FakeCluster(jm_deployment(**{"spec.selector": {"matchLabels": {"app": "doops-agent"},
                                                    "matchExpressions": [{"key": "x", "operator": "Exists"}]}})),
     "matchLabels only"),
    ("other helm owner",
     FakeCluster(jm_deployment(**{"metadata.annotations": {"meta.helm.sh/release-name": "other"}})), "owned by Helm"),
])
def test_preflight_refuses_before_any_mutation(name, cluster, error):
    module = load()
    with pytest.raises(module.AdoptError, match=error):
        adopt(module, cluster)
    assert cluster.mutations() == [], name


def test_preflight_rejects_wrong_gateway_identity_and_unpinned_image():
    module = load()
    for kwargs, error in (({"instance": "other"}, "Gateway identity"), ({"image": "repo/doops:latest"}, "pinned")):
        cluster = FakeCluster()
        with pytest.raises(module.AdoptError, match=error):
            adopt(module, cluster, **kwargs)
        assert cluster.mutations() == []


def test_plan_reports_without_mutating():
    module = load()
    cluster = FakeCluster()
    plan = adopt(module, cluster, plan_only=True)
    assert plan["phase"] == "plan"
    assert plan["serviceAccount"] == "doops-agent"
    assert "settingsSource" not in plan and "secret" not in plan
    assert plan["keptVolumes"] == []
    assert cluster.mutations() == []
    assert SETTINGS_CANARY not in json.dumps(plan)


def test_run_backs_up_before_mutation_and_never_prints_settings(capsys):
    module = load()
    cluster = FakeCluster()
    result = adopt(module, cluster)
    assert result["phase"] == "adopted" and result["podUID"] == "new-pod-uid"
    backup = cluster.index("apply -f -")
    assert backup < cluster.index("label deployment") < cluster.index("helm upgrade --install")
    first_apply = cluster.applied[0]
    assert first_apply["kind"] == "ConfigMap" and "deployment.json" in first_apply["data"]
    assert SETTINGS_CANARY not in json.dumps(first_apply)
    # The model provider comes from the Gateway: no settings Secret is created
    # and the legacy settings object is never read.
    assert not any(obj["kind"] == "Secret" for obj in cluster.applied)
    assert not any("doops-agent-settings" in " ".join(c) for c in cluster.calls)
    assert SETTINGS_CANARY not in json.dumps(result) + capsys.readouterr().out


def test_verification_failure_restores_backup():
    module = load()
    cluster = FakeCluster()

    def failing(*_args, **_kwargs):
        raise module.AdoptError("new Pod is not ready")

    with pytest.raises(module.AdoptError, match="backup restored"):
        adopt(module, cluster, verifier=failing)
    assert cluster.index("helm upgrade --install") < cluster.index("delete secret")
    assert cluster.index("delete secret") < cluster.index("replace --force")
    assert not any(c[:2] == ["helm", "uninstall"] for c in cluster.calls)


def test_restore_failure_is_reported_explicitly():
    module = load()
    cluster = FakeCluster(fail={"replace --force": "apiserver unavailable"})

    def failing(*_args, **_kwargs):
        raise module.AdoptError("new Pod is not ready")

    with pytest.raises(module.AdoptError, match="automatic restore failed"):
        adopt(module, cluster, verifier=failing)


@pytest.mark.parametrize("host_access", [None, True, False])
def test_generated_values_render_with_the_real_chart(tmp_path, host_access):
    module = load()
    deployment = jm_deployment()
    repository, digest = module.parse_image(IMAGE)
    values = module.chart_values(deployment, "doops-agent", repository, digest)
    assert "doagentSettings" not in values and "modelRouting" not in values
    if host_access is not None:
        values["hostAccess"] = {"enabled": host_access}
    path = tmp_path / "values.json"
    path.write_text(json.dumps(values))
    result = subprocess.run(["helm", "template", "doops-agent", str(CHART), "-n", "doops-system", "-f", str(path)],
                            capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    rendered = [d for d in yaml.safe_load_all(result.stdout) if d][0]
    pod = rendered["spec"]["template"]["spec"]
    assert rendered["spec"]["selector"]["matchLabels"] == {"app": "doops-agent"}
    assert pod["serviceAccountName"] == "doops-agent"
    container = pod["containers"][0]
    assert container["image"] == IMAGE
    env = {e["name"]: e["value"] for e in container["env"]}
    assert env["DOOPS_KUBECONFIG_SOURCE"] == "serviceAccount"
    assert env["DOOPS_GATEWAY_CLUSTER"] == "doops-jm" and env["DOOPS_GATEWAY_INSTANCE"] == "jm-228"
    assert "DO_AGENT_MODEL_ROUTING_POLICY" not in env
    assert pod["hostPID"] is (host_access is not False)
    if host_access is False:
        assert not pod.get("volumes")
        assert not container.get("volumeMounts")
    else:
        assert pod["volumes"] == [
            {"name": "node-root", "hostPath": {"path": "/", "type": "Directory"}}
        ]
        assert container["volumeMounts"] == [
            {"name": "node-root", "mountPath": "/host", "readOnly": True}
        ]


@pytest.mark.parametrize("hostname,want", [
    ("", "sha256:" + IMAGE.rsplit("sha256:", 1)[1]),
    ("doops-self-upgrade-abc", CONTROLLER_DIGEST),
])
def test_verifier_expects_the_controller_runtime_digest(monkeypatch, hostname, want):
    module = load()
    monkeypatch.setenv("HOSTNAME", hostname)
    seen = {}

    def verifier(*_args, **kwargs):
        seen["digest"] = kwargs.get("runtime_digest")
        return "new-pod-uid"

    assert adopt(module, FakeCluster(), verifier=verifier)["phase"] == "adopted"
    assert seen["digest"] == want


def test_missing_controller_pod_falls_back_to_the_image_digest(monkeypatch):
    module = load()
    monkeypatch.setenv("HOSTNAME", "gone")
    seen = {}

    def verifier(*_args, **kwargs):
        seen["digest"] = kwargs.get("runtime_digest")
        return "new-pod-uid"

    adopt(module, FakeCluster(fail={"get pod gone": "pod not found"}), verifier=verifier)
    assert seen["digest"] == "sha256:" + IMAGE.rsplit("sha256:", 1)[1]
