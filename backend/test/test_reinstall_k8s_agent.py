import importlib.util
import io
import json
import urllib.error
from email.message import Message
from pathlib import Path

import pytest

from test_adopt_agent import IMAGE, SETTINGS_CANARY, jm_deployment

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "reinstall-k8s-agent.py"
RUNTIME_DIGEST = "sha256:" + "d" * 64

_MODULE = None


def load():
    global _MODULE
    if _MODULE is None:
        spec = importlib.util.spec_from_file_location("reinstall_k8s_agent", SCRIPT)
        _MODULE = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(_MODULE)
    return _MODULE


def not_found(kind, name):
    return load().ReinstallError(f"kubectl -n doops-system get failed: Error from server (NotFound): {kind} {name} not found")


def stuck_pod(name):
    return {"metadata": {"name": name, "labels": {"app": "doops-agent"},
                         "deletionTimestamp": "2026-09-01T00:00:00Z"}}


class FakeCluster:
    """kubectl/helm boundary for the gw-jm shape: a broken Agent plus an adoption leftover."""

    def __init__(self, deployment="live", releases=(), configmaps=None, serviceaccounts=("doops-agent",),
                 pods=None, fail=None):
        self.objects = {
            "deployment": {"doops-agent": jm_deployment()} if deployment == "live" else {},
            "configmap": {"doops-agent-settings": {"data": {"settings.json": SETTINGS_CANARY}}},
            "serviceaccount": {name: {} for name in serviceaccounts},
            "secret": {},
        }
        self.objects["configmap"].update(configmaps or {})
        self.releases = list(releases)
        self.lists = {
            "pods": pods if pods is not None else [
                stuck_pod("doops-agent-57f4d9d5f4-957bx"),
                {"metadata": {"name": "doops-agent-6c8848bd9-nlbp9", "labels": {"app": "doops-agent"}}},
                {"metadata": {"name": "doops-adopt-run", "labels": {}}},
                {"metadata": {"name": "unrelated-app", "labels": {"app": "other"}}},
            ],
            "replicasets": [{"metadata": {"name": "doops-agent-6c8848bd9", "labels": {"app": "doops-agent"}}}],
            "jobs": [{"metadata": {"name": "doops-self-upgrade-abc", "labels": {}}}],
        }
        self.fail = fail or {}
        self.calls = []
        self.applied = []

    def __call__(self, args, stdin=None, timeout=120):
        self.calls.append(args)
        text = " ".join(args)
        for marker, message in self.fail.items():
            if marker in text:
                raise load().ReinstallError(message)
        if args[:2] == ["helm", "list"]:
            return json.dumps([{"name": r} for r in self.releases])
        if args[:3] == ["helm", "get", "values"]:
            return "{}"
        if args[0] == "helm":
            return ""
        verb, rest = args[3], args[4:]
        if verb == "get":
            kind = rest[0]
            if kind in self.lists:
                return json.dumps({"items": self.lists[kind]})
            name = rest[1]
            if name not in self.objects[kind]:
                raise not_found(kind, name)
            return json.dumps(self.objects[kind][name])
        if verb == "apply":
            obj = json.loads(stdin)
            self.applied.append(obj)
            self.objects[obj["kind"].lower()][obj["metadata"]["name"]] = obj
        return ""

    def index(self, needle):
        return next(i for i, call in enumerate(self.calls) if needle in " ".join(call))

    def mutations(self):
        return [c for c in self.calls if (c[0] == "helm" and c[1] in ("upgrade", "uninstall"))
                or any(v in c for v in ("apply", "delete", "replace", "label", "annotate"))]


def reinstall(module, cluster, **kwargs):
    module.A.run = cluster
    module.resolve_image = lambda image: IMAGE
    params = dict(namespace="doops-system", workload="doops-agent", release="doops-agent",
                  cluster="doops-jm", instance="jm-228", image="docker.cnb.cool/l8ai/ai/doops.sh:latest",
                  checker=lambda *a, **k: (RUNTIME_DIGEST, "release=v1.3.78 commit=" + "e" * 40),
                  deleter=lambda *a, **k: cluster.calls.append(["deleter"]),
                  verifier=lambda *a, **k: "new-pod-uid")
    params.update(kwargs)
    return module.reinstall(**params)


@pytest.fixture
def module():
    module = load()
    resolve = module.resolve_image
    yield module
    module.resolve_image = resolve


# --- refusals before any mutation -------------------------------------------

@pytest.mark.parametrize("name,cluster,kwargs,error", [
    ("wrong gateway identity", FakeCluster(), {"instance": "other"}, "Gateway identity"),
    ("missing service account", FakeCluster(serviceaccounts=()), {}, "serviceaccount/doops-agent must exist"),
    ("missing settings", FakeCluster(), {"_drop": "doops-agent-settings"}, "doops-agent-settings with key"),
    ("no deployment and no backup", FakeCluster(deployment=None), {}, "no Deployment doops-agent"),
    ("other helm owner", FakeCluster(), {"_owner": "someone-else"}, "belongs to Helm release someone-else"),
])
def test_prepare_refuses_before_any_mutation(module, name, cluster, kwargs, error):
    if kwargs.pop("_drop", None):
        del cluster.objects["configmap"]["doops-agent-settings"]
    owner = kwargs.pop("_owner", None)
    if owner:
        cluster.objects["deployment"]["doops-agent"]["metadata"]["annotations"] = {
            "meta.helm.sh/release-name": owner}
    with pytest.raises(module.ReinstallError, match=error):
        reinstall(module, cluster, **kwargs)
    assert cluster.mutations() == [], name


def test_failed_pull_check_deletes_nothing(module):
    cluster = FakeCluster()

    def failing(*_a, **_k):
        raise module.ReinstallError("image cannot start on the cluster (ImagePullBackOff)")

    with pytest.raises(module.ReinstallError, match="ImagePullBackOff"):
        reinstall(module, cluster, checker=failing)
    assert ["deleter"] not in cluster.calls
    assert cluster.mutations() == []


# --- plan and source selection ----------------------------------------------

def test_plan_lists_leftovers_without_mutating(module):
    cluster = FakeCluster()
    plan = reinstall(module, cluster, plan_only=True)
    assert plan["phase"] == "plan" and plan["image"] == IMAGE
    assert plan["source"] == "deployment/doops-agent"
    assert plan["settingsSource"] == "configmap/doops-agent-settings"
    delete = plan["delete"]
    assert delete["deployment"] == "doops-agent" and delete["helmRelease"] == ""
    assert delete["pods"] == ["doops-agent-57f4d9d5f4-957bx", "doops-agent-6c8848bd9-nlbp9", "doops-adopt-run"]
    assert delete["stuckPods"] == ["doops-agent-57f4d9d5f4-957bx"]
    assert delete["replicasets"] == ["doops-agent-6c8848bd9"]
    assert delete["jobs"] == ["doops-self-upgrade-abc"]
    assert cluster.mutations() == []
    assert SETTINGS_CANARY not in json.dumps(plan)


@pytest.mark.parametrize("configmaps,want", [
    ({}, "deployment/doops-agent"),
    ({"doops-adopt-backup-doops-agent": {"data": {"deployment.json": json.dumps(jm_deployment())}}},
     "configmap/doops-adopt-backup-doops-agent"),
    ({"doops-adopt-backup-doops-agent": {"data": {"deployment.json": "{}"}},
      "doops-reinstall-backup-doops-agent": {"data": {"source.json": json.dumps(jm_deployment())}}},
     "configmap/doops-reinstall-backup-doops-agent"),
])
def test_source_prefers_the_last_known_good_backup(module, configmaps, want):
    plan = reinstall(module, FakeCluster(configmaps=configmaps), plan_only=True)
    assert plan["source"] == want


def test_backup_rebuilds_a_deleted_deployment(module):
    backup = {"doops-adopt-backup-doops-agent": {"data": {"deployment.json": json.dumps(jm_deployment())}}}
    plan = reinstall(module, FakeCluster(deployment=None, configmaps=backup), plan_only=True)
    assert plan["delete"]["deployment"] == ""


# --- run ----------------------------------------------------------------------

def test_run_orders_check_backup_delete_install(module, capsys, tmp_path):
    cluster = FakeCluster(releases=["doops-agent"])
    seen = {}

    def verifier(namespace, workload, image, runtime_digest=None):
        seen.update(image=image, digest=runtime_digest)
        return "new-pod-uid"

    result = reinstall(module, cluster, verifier=verifier, backup_dir=str(tmp_path / "bk"))
    assert result["phase"] == "installed" and result["podUID"] == "new-pod-uid"
    assert seen == {"image": IMAGE, "digest": RUNTIME_DIGEST}
    order = [cluster.index("doops-reinstall-backup") if False else cluster.calls.index(next(
        c for c in cluster.calls if c[3:4] == ["apply"])), cluster.calls.index(["deleter"]),
        cluster.index("helm upgrade --install doops-agent")]
    assert order == sorted(order)
    assert cluster.applied[0]["metadata"]["name"] == "doops-reinstall-backup-doops-agent"
    secret = next(o for o in cluster.applied if o["kind"] == "Secret")
    assert secret["metadata"]["name"] == "doops-agent-doagent-settings"
    dry, real = [c for c in cluster.calls if c[:2] == ["helm", "upgrade"]]
    assert "--dry-run=server" in dry and "--dry-run=server" not in real
    files = {p.name for p in (tmp_path / "bk").iterdir()}
    assert files == {"source.json", "inventory.json", "live.json", "helm-values.json"}
    assert all(oct(p.stat().st_mode)[-3:] == "600" for p in (tmp_path / "bk").iterdir())
    printed = capsys.readouterr()
    for text in (json.dumps(result), json.dumps(cluster.applied[0]), printed.out, printed.err):
        assert SETTINGS_CANARY not in text
    assert all(SETTINGS_CANARY not in p.read_text() for p in (tmp_path / "bk").iterdir())


@pytest.mark.parametrize("stage", ["helm upgrade --install", "verify"])
def test_failure_after_delete_restores_the_backup(module, stage):
    cluster = FakeCluster(fail={"helm upgrade --install": "schema rejected"} if stage != "verify" else {})

    def verifier(*_a, **_k):
        raise module.ReinstallError("verification failed: new Pod is not ready")

    with pytest.raises(module.ReinstallError, match="backup restored"):
        reinstall(module, cluster, verifier=verifier)
    restored = cluster.applied[-1]
    assert restored["kind"] == "Deployment" and restored["metadata"]["name"] == "doops-agent"
    assert "resourceVersion" not in restored["metadata"]


def test_restore_failure_is_reported(module):
    cluster = FakeCluster()

    def verifier(*_a, **_k):
        cluster.fail["apply -f -"] = "apiserver unavailable"
        raise module.ReinstallError("verification failed")

    with pytest.raises(module.ReinstallError, match="automatic restore failed: apiserver unavailable"):
        reinstall(module, cluster, verifier=verifier)


# --- delete -------------------------------------------------------------------

def test_delete_old_force_removes_only_pods_still_present(module):
    cluster = FakeCluster(releases=["doops-agent"])
    module.A.run = cluster
    found = {"helmRelease": "doops-agent", "pods": ["doops-agent-57f4d9d5f4-957bx", "gone-pod"],
             "replicasets": ["doops-agent-6c8848bd9"], "jobs": []}
    clock = iter(range(0, 1000, 30))
    module.delete_old("doops-system", "doops-agent", found, clock=lambda: next(clock), sleep=lambda _s: None)
    assert cluster.index("helm uninstall doops-agent") < cluster.index("delete deployment doops-agent")
    forced = [c for c in cluster.calls if "--force" in c]
    assert forced == [["kubectl", "-n", "doops-system", "delete", "pods", "doops-agent-57f4d9d5f4-957bx",
                       "--grace-period=0", "--force", "--ignore-not-found"]]
    assert not any("jobs" in c and "delete" in c for c in cluster.calls)


# --- image resolution and pull check ------------------------------------------

class Response(io.BytesIO):
    def __init__(self, body=b"", headers=None):
        super().__init__(body)
        self.headers = headers or {}

    def __enter__(self):
        return self

    def __exit__(self, *_exc):
        return False


def registry(digest="sha256:" + "f" * 64, token=b'{"token":"t"}'):
    calls = []

    def urlopen(request, timeout=30):
        calls.append(request)
        if isinstance(request, str):
            assert "scope=repository%3Al8ai%2Fai%2Fdoops.sh%3Apull" in request
            return Response(token)
        if "Authorization" not in request.headers:
            headers = Message()
            headers["WWW-Authenticate"] = 'Bearer realm="https://docker.cnb.cool/service/token",service="cnb-registry"'
            raise urllib.error.HTTPError(request.full_url, 401, "unauthorized", headers, None)
        return Response(headers={"Docker-Content-Digest": digest})

    return urlopen, calls


@pytest.mark.parametrize("image,want", [
    ("docker.cnb.cool/l8ai/ai/doops.sh:latest", "docker.cnb.cool/l8ai/ai/doops.sh@sha256:" + "f" * 64),
    ("docker.cnb.cool/l8ai/ai/doops.sh", "docker.cnb.cool/l8ai/ai/doops.sh@sha256:" + "f" * 64),
    (IMAGE, IMAGE),
])
def test_resolve_image_pins_tags_to_the_registry_digest(image, want):
    urlopen, _calls = registry()
    assert load().resolve_image(image, urlopen=urlopen) == want


@pytest.mark.parametrize("image,urlopen,error", [
    ("doops.sh:latest", registry()[0], "registry host"),
    ("docker.cnb.cool/l8ai/ai/doops.sh:latest", registry(digest="")[0], "pinned"),
    ("docker.cnb.cool/l8ai/ai/doops.sh:latest", registry(token=b"{}")[0], "no token"),
])
def test_resolve_image_rejects_unpinnable_references(image, urlopen, error):
    with pytest.raises(load().ReinstallError, match=error):
        load().resolve_image(image, urlopen=urlopen)


def pullcheck_cluster(phases):
    phases = iter(phases)
    calls = []

    def run(args, stdin=None, timeout=120):
        calls.append(args)
        if args[3] == "get" and args[4] == "pod":
            phase, reason = next(phases)
            status = {"phase": phase, "containerStatuses": [{
                "name": "pullcheck", "imageID": "docker.cnb.cool/l8ai/ai/doops.sh@" + RUNTIME_DIGEST,
                "state": {"waiting": {"reason": reason}} if reason else {}}]}
            return json.dumps({"status": status})
        if args[3] == "logs":
            return "release=v1.3.78 commit=" + "e" * 40 + "\n"
        return "Warning Failed pod/doops-reinstall-pullcheck dial tcp: i/o timeout" if args[3] == "get" else ""

    return run, calls


@pytest.mark.parametrize("phases,want,error", [
    ([("Pending", "ContainerCreating"), ("Succeeded", "")], RUNTIME_DIGEST, None),
    ([("Pending", "ImagePullBackOff")] * 6, None, "ImagePullBackOff"),
    ([("Failed", "")], None, "exited non-zero"),
])
def test_pull_check_reports_the_runtime_digest_or_the_pull_error(phases, want, error):
    module = load()
    module.A.run, calls = pullcheck_cluster(phases)
    pod_spec = {"nodeSelector": {"kubernetes.io/hostname": "jm-228"}, "imagePullSecrets": [{"name": "cnb"}]}
    if error:
        with pytest.raises(module.ReinstallError, match=error):
            module.pull_check("doops-system", IMAGE, pod_spec, clock=lambda: 0, sleep=lambda _s: None)
    else:
        digest, line = module.pull_check("doops-system", IMAGE, pod_spec, clock=lambda: 0, sleep=lambda _s: None)
        assert digest == want and line.startswith("release=v1.3.78")
    manifest = json.loads(next(c for c in calls if c[3] == "apply") and module.json.dumps(
        module.pullcheck_manifest("doops-system", IMAGE, pod_spec)))
    assert manifest["spec"]["nodeSelector"] == pod_spec["nodeSelector"]
    assert manifest["spec"]["imagePullSecrets"] == pod_spec["imagePullSecrets"]
    assert manifest["spec"]["automountServiceAccountToken"] is False
    assert calls[-1][3:5] == ["delete", "pod"]
