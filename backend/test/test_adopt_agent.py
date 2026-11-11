import importlib.util
import hashlib
import json
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[1]
MODULE = ROOT / "agent" / "adopt_agent.py"
CHART = ROOT / "deploy" / "helm" / "doops-agent"
DIGEST = "sha256:" + "a" * 64
IMAGE = "registry.example.com/doops-agent@" + DIGEST
SETTINGS_CANARY = '{"apiKey":"synthetic-settings-canary"}'


_MODULE = None
CONTROLLER_DIGEST = "sha256:" + "c" * 64
COMMIT = "a" * 40
OLD_COMMIT = "b" * 40


def load():
    # One module instance, so AdoptError raised by fakes is the class the script catches.
    global _MODULE
    if _MODULE is None:
        spec = importlib.util.spec_from_file_location("adopt_agent", MODULE)
        _MODULE = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(_MODULE)
    return _MODULE


@pytest.fixture(autouse=True)
def upgrade_environment(monkeypatch, tmp_path):
    for key, value in {
        "DOOPS_UPGRADE_OPERATION_ID": "adopt-test",
        "DOOPS_UPGRADE_EXPECTED_RELEASE_COMMIT": COMMIT,
        "DOOPS_UPGRADE_NAMESPACE": "doops-system", "DOOPS_UPGRADE_RELEASE": "doops-agent",
        "DOOPS_UPGRADE_WORKLOAD": "doops-agent", "DOOPS_UPGRADE_IMAGE": IMAGE,
        "DOOPS_UPGRADE_PHASE": "apply", "DOOPS_UPGRADE_REQUESTER_ID": "test-user",
        "DOOPS_UPGRADE_SESSION_ID": "adopt-test", "DOOPS_UPGRADE_AUDIT_ID": "42",
        "DOOPS_UPGRADE_JOB": "doops-self-upgrade-" + hashlib.sha256(b"adopt-test").hexdigest()[:24] + "-apply",
        "HOSTNAME": "controller", "TMPDIR": str(tmp_path),
    }.items():
        monkeypatch.setenv(key, value)
    monkeypatch.setattr(load().time, "sleep", lambda _: None)


def demo_deployment(**overrides):
    """Shape of the live gw-demo unmanaged Agent Deployment."""
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": "doops-agent", "namespace": "doops-system", "uid": "u1",
                     "resourceVersion": "9", "generation": 1, "labels": {"app": "doops-agent"}},
        "status": {"observedGeneration": 1, "updatedReplicas": 1, "readyReplicas": 1, "availableReplicas": 1},
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": {"app": "doops-agent"}},
            "template": {
                "metadata": {"labels": {"app": "doops-agent"}},
                "spec": {
                    "serviceAccountName": "doops-agent",
                    "containers": [{
                        "name": "doops-agent",
                        "image": "registry.example.com/doops-agent@sha256:" + "2" * 64,
                        "env": [
                            {"name": "DOOPS_GATEWAY_URL", "value": "https://doops.l8ai.cn"},
                            {"name": "DOOPS_GATEWAY_CLUSTER", "value": "doops-demo"},
                            {"name": "DOOPS_GATEWAY_INSTANCE", "value": "demo-228"},
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
        self.deployment = deployment or demo_deployment()
        self.releases = list(releases)
        self.fail = fail or {}
        self.calls = []
        self.applied = []
        self.backup = None
        self.receipt = None

    def __call__(self, args, stdin=None, timeout=120):
        self.calls.append(args)
        text = " ".join(args)
        for marker, message in self.fail.items():
            if marker in text:
                raise load().AdoptError(message)
        if args[:2] == ["helm", "list"]:
            return json.dumps([{"name": r} for r in self.releases])
        if args[:2] == ["helm", "history"]:
            return '[{"revision":1,"status":"deployed"}]'
        if args[:2] == ["helm", "upgrade"]:
            if "--dry-run=server" not in args:
                self.deployment["spec"]["template"]["spec"]["containers"][0]["image"] = IMAGE
            return ""
        if args[0] == "helm":
            return ""
        if "get" in args and "deployment" in args:
            return json.dumps(self.deployment)
        if "get" in args and "configmap" in args and "doops-agent-settings" in args:
            return json.dumps({"data": {"settings.json": SETTINGS_CANARY}})
        if "get" in args and "configmap" in args:
            return json.dumps(self.receipt) if self.receipt else ""
        if "get" in args and "secret" in args:
            return json.dumps(self.backup)
        if "get" in args and "secrets" in args:
            return json.dumps({"items": [{"metadata": {"name": "sh.helm.release.v1.doops-agent.v1"}}]})
        if "get" in args and "pod" in args:
            return json.dumps({"spec": {"nodeName": "node-a", "containers": [{"name": "controller", "image": IMAGE}]},
                               "status": {"containerStatuses": [
                {"name": "controller", "ready": True, "imageID": "registry.example/doops@" + CONTROLLER_DIGEST}]}})
        if "get" in args and "pods" in args:
            image = self.deployment["spec"]["template"]["spec"]["containers"][0]["image"]
            return json.dumps({"items": [{"metadata": {"name": "agent-pod", "uid": "new-pod-uid"},
                "spec": {"nodeName": "node-a", "containers": [{"name": "doops-agent", "image": image}]},
                "status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}],
                           "containerStatuses": [{"name": "doops-agent", "ready": True, "imageID": image}]}}]})
        if "/app/doops-agent -version" in text:
            return "release=test commit=" + OLD_COMMIT
        if "create" in args or "apply" in args or "replace" in args:
            obj = json.loads(stdin)
            self.applied.append(obj)
            if "create" in args:
                self.backup = obj
            elif "replace" in args:
                self.deployment = obj
                self.deployment["metadata"]["generation"] = 1
                self.deployment["status"] = demo_deployment()["status"]
            else:
                self.receipt = obj
        return ""

    def index(self, needle):
        return next(i for i, call in enumerate(self.calls) if needle in " ".join(call))

    def mutations(self):
        return [c for c in self.calls if c[0] == "helm" and c[1] == "upgrade"
                or any(v in c for v in ("create", "apply", "label", "annotate", "replace", "delete"))]


def verified(_namespace, _workload, image, **kwargs):
    return {"podUID": "new-pod-uid", "node": "node-a", "runtimeClass": "",
            "runtimeDigest": kwargs["runtime_digest"],
            "release_commit": COMMIT if image == IMAGE else OLD_COMMIT}


def adopt(module, cluster, **kwargs):
    module.run = cluster
    params = dict(namespace="doops-system", workload="doops-agent", release="doops-agent",
                  cluster="doops-demo", instance="demo-228", image=IMAGE,
                  verifier=verified)
    params.update(kwargs)
    return module.adopt(**params)


@pytest.mark.parametrize("name,cluster,error", [
    ("existing release", FakeCluster(releases=["doops-agent"]), "already exists"),
    ("two replicas", FakeCluster(demo_deployment(**{"spec.replicas": 2})), "exactly one replica"),
    ("default service account",
     FakeCluster(demo_deployment(**{"spec.template.spec.serviceAccountName": "default"})), "dedicated ServiceAccount"),
    ("selector expressions",
     FakeCluster(demo_deployment(**{"spec.selector": {"matchLabels": {"app": "doops-agent"},
                                                    "matchExpressions": [{"key": "x", "operator": "Exists"}]}})),
     "matchLabels only"),
    ("other helm owner",
     FakeCluster(demo_deployment(**{"metadata.annotations": {"meta.helm.sh/release-name": "other"}})), "owned by Helm"),
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
    assert result["phase"] == "apply" and result["podUID"] == "new-pod-uid"
    backup = cluster.index("create -f -")
    assert backup < cluster.index("label deployment") < cluster.index("helm upgrade --install")
    first_apply = cluster.applied[0]
    assert first_apply["kind"] == "Secret" and "deployment.json" in first_apply["data"]
    assert SETTINGS_CANARY not in json.dumps(first_apply)
    # Only the rollback backup is a Secret; legacy model settings are never read.
    assert [obj for obj in cluster.applied if obj["kind"] == "Secret"] == [first_apply]
    assert not any("doops-agent-settings" in " ".join(c) for c in cluster.calls)
    assert SETTINGS_CANARY not in json.dumps(result) + capsys.readouterr().out


def test_verification_failure_restores_backup():
    module = load()
    cluster = FakeCluster()

    def failing(namespace, workload, image, **kwargs):
        if image == IMAGE:
            raise module.AdoptError("new Pod is not ready")
        return verified(namespace, workload, image, **kwargs)

    with pytest.raises(module.AdoptError, match="backup restored"):
        adopt(module, cluster, verifier=failing)
    assert cluster.index("helm upgrade --install") < cluster.index("delete secret")
    assert cluster.index("delete secret") < cluster.index("replace --force")
    assert not any(c[:2] == ["helm", "uninstall"] for c in cluster.calls)


def test_restore_failure_is_reported_explicitly():
    module = load()
    cluster = FakeCluster(fail={"replace --force": "apiserver unavailable"})

    def failing(namespace, workload, image, **kwargs):
        if image == IMAGE:
            raise module.AdoptError("new Pod is not ready")
        return verified(namespace, workload, image, **kwargs)

    with pytest.raises(module.AdoptError, match="automatic restore failed"):
        adopt(module, cluster, verifier=failing)


@pytest.mark.parametrize("host_access", [None, True, False])
def test_generated_values_render_with_the_real_chart(tmp_path, host_access):
    module = load()
    deployment = demo_deployment()
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
    assert env["DOOPS_GATEWAY_CLUSTER"] == "doops-demo" and env["DOOPS_GATEWAY_INSTANCE"] == "demo-228"
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


def test_verifier_expects_the_controller_runtime_digest(monkeypatch):
    module = load()
    monkeypatch.setenv("HOSTNAME", "doops-self-upgrade-abc")
    seen = {}

    def verifier(*args, **kwargs):
        seen["digest"] = kwargs.get("runtime_digest")
        return verified(*args, **kwargs)

    assert adopt(module, FakeCluster(), verifier=verifier)["phase"] == "apply"
    assert seen["digest"] == CONTROLLER_DIGEST


@pytest.mark.parametrize("hostname", ["", "gone"])
def test_missing_controller_identity_fails_and_restores(monkeypatch, hostname):
    module = load()
    monkeypatch.setenv("HOSTNAME", hostname)
    cluster = FakeCluster(fail={"get pod gone": "pod not found"})
    with pytest.raises(module.AdoptError, match="controller_identity_unverified"):
        adopt(module, cluster)
    assert cluster.index("helm upgrade --install") < cluster.index("replace --force")
