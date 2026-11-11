import importlib.util
import io
import json
import os
import subprocess
import sys
import urllib.error
from email.message import Message
from pathlib import Path

import pytest

from test_adopt_agent import IMAGE, SETTINGS_CANARY, demo_deployment

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "reinstall-k8s-agent.py"

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
    """kubectl/helm boundary for the gw-demo shape: a broken Agent plus an adoption leftover."""

    def __init__(self, deployment="live", releases=(), configmaps=None, serviceaccounts=("doops-agent",),
                 pods=None, fail=None):
        self.objects = {
            "deployment": {"doops-agent": demo_deployment()} if deployment == "live" else {},
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

    def __call__(self, args, stdin=None, timeout=120):
        self.calls.append(args)
        assert stdin is None, "inspection must not submit resource manifests"
        text = " ".join(args)
        for marker, message in self.fail.items():
            if marker in text:
                raise load().ReinstallError(message)
        if args[:2] == ["helm", "list"]:
            return json.dumps([{"name": r} for r in self.releases])
        assert args[:1] == ["kubectl"], f"unexpected local tool: {args}"
        verb, rest = args[3], args[4:]
        assert verb == "get", f"inspection attempted mutation: {args}"
        if verb == "get":
            kind = rest[0]
            if kind in self.lists:
                return json.dumps({"items": self.lists[kind]})
            name = rest[1]
            if name not in self.objects[kind]:
                if "--ignore-not-found" in args:
                    return ""
                raise not_found(kind, name)
            return json.dumps(self.objects[kind][name])

    def mutations(self):
        return [c for c in self.calls if (c[0] == "helm" and c[1] in ("upgrade", "uninstall"))
                or any(v in c for v in ("apply", "delete", "replace", "label", "annotate"))]


def plan(module, cluster, **kwargs):
    module.A.run = cluster
    module.resolve_image = lambda image: IMAGE
    params = dict(namespace="doops-system", workload="doops-agent", release="doops-agent",
                  cluster="doops-demo", instance="demo-228", image="registry.example.com/doops-agent:latest")
    params.update(kwargs)
    return module.plan(**params)


@pytest.fixture
def module():
    module = load()
    resolve, run = module.resolve_image, module.A.run
    yield module
    module.resolve_image = resolve
    module.A.run = run


# --- refusals before any mutation -------------------------------------------

@pytest.mark.parametrize("name,cluster,kwargs,error", [
    ("wrong gateway identity", FakeCluster(), {"instance": "other"}, "Gateway identity"),
    ("missing service account", FakeCluster(serviceaccounts=()), {}, "serviceaccount/doops-agent must exist"),
    ("no deployment and no backup", FakeCluster(deployment=None), {}, "no Deployment doops-agent"),
    ("other helm owner", FakeCluster(), {"_owner": "someone-else"}, "belongs to Helm release someone-else"),
])
def test_prepare_refuses_before_any_mutation(module, name, cluster, kwargs, error):
    owner = kwargs.pop("_owner", None)
    if owner:
        cluster.objects["deployment"]["doops-agent"]["metadata"]["annotations"] = {
            "meta.helm.sh/release-name": owner}
    with pytest.raises(module.ReinstallError, match=error):
        plan(module, cluster, **kwargs)
    assert cluster.mutations() == [], name


# --- plan and source selection ----------------------------------------------

def test_plan_lists_leftovers_without_mutating(module):
    cluster = FakeCluster()
    result = plan(module, cluster)
    assert result["phase"] == "plan" and result["image"] == IMAGE
    assert result["readOnly"] is True and result["mutationSupported"] is False
    assert result["source"] == "deployment/doops-agent"
    # The model provider comes from the Gateway; reinstall reads no settings object.
    assert "settingsSource" not in result
    inventory = result["inventory"]
    assert inventory["deployment"] == "doops-agent" and inventory["helmRelease"] == ""
    assert inventory["pods"] == ["doops-agent-57f4d9d5f4-957bx", "doops-agent-6c8848bd9-nlbp9", "doops-adopt-run"]
    assert inventory["stuckPods"] == ["doops-agent-57f4d9d5f4-957bx"]
    assert inventory["replicasets"] == ["doops-agent-6c8848bd9"]
    assert inventory["jobs"] == ["doops-self-upgrade-abc"]
    assert cluster.mutations() == []
    assert SETTINGS_CANARY not in json.dumps(result)
    assert not any("secret" in call for call in cluster.calls)


@pytest.mark.parametrize("configmaps,want", [
    ({}, "deployment/doops-agent"),
    ({"doops-adopt-backup-doops-agent": {"data": {"deployment.json": json.dumps(demo_deployment())}}},
     "configmap/doops-adopt-backup-doops-agent"),
    ({"doops-adopt-backup-doops-agent": {"data": {"deployment.json": "{}"}},
      "doops-reinstall-backup-doops-agent": {"data": {"source.json": json.dumps(demo_deployment())}}},
     "configmap/doops-reinstall-backup-doops-agent"),
])
def test_source_prefers_the_last_known_good_backup(module, configmaps, want):
    result = plan(module, FakeCluster(configmaps=configmaps))
    assert result["source"] == want


def test_backup_can_be_inspected_after_deployment_is_deleted(module):
    backup = {"doops-adopt-backup-doops-agent": {"data": {"deployment.json": json.dumps(demo_deployment())}}}
    cluster = FakeCluster(deployment=None, configmaps=backup)
    result = plan(module, cluster)
    assert result["inventory"]["deployment"] == ""
    assert result["source"] == "configmap/doops-adopt-backup-doops-agent"
    assert cluster.mutations() == []


@pytest.mark.parametrize("error", ["Forbidden", "API connection refused", "resource not found in authorized scope"])
def test_inspection_errors_are_not_treated_as_absent_backups(module, error):
    cluster = FakeCluster(fail={"get configmap": error})
    with pytest.raises(module.ReinstallError, match=error):
        plan(module, cluster)
    assert cluster.calls[-1][4] == "configmap"
    assert cluster.mutations() == []


def test_new_controller_backup_naming_is_not_used_for_legacy_inspection(module, monkeypatch):
    def unexpected(*args):
        pytest.fail("legacy ConfigMap inspection must not depend on upgrade operation context")

    monkeypatch.setattr(module.A, "backup_name", unexpected)
    assert plan(module, FakeCluster())["source"] == "deployment/doops-agent"


# --- retired mutation entrypoints -------------------------------------------

@pytest.mark.parametrize("action", ["run", "restore"])
@pytest.mark.parametrize("complete_args", [False, True])
def test_retired_actions_fail_before_io_or_credential_selection(module, monkeypatch, tmp_path, capsys,
                                                               action, complete_args):
    def unexpected(*args, **kwargs):
        pytest.fail("retired mutation reached network, inspection or an external command")

    monkeypatch.setattr(module, "plan", unexpected)
    monkeypatch.setattr(module, "resolve_image", unexpected)
    monkeypatch.setattr(module.A, "run", unexpected)
    monkeypatch.setenv("KUBECONFIG", "unchanged-inspection-config")
    backup_dir = tmp_path / "must-not-be-created"
    args = [action]
    if complete_args:
        args += [
            "--cluster", "doops-demo", "--instance", "demo-228",
            "--kubeconfig", "SYNTHETIC_KUBECONFIG_MUST_NOT_BE_USED",
            "--backup-dir", str(backup_dir),
            "--image", "https://must-not-be-contacted.example/image",
        ]
    with pytest.raises(SystemExit) as exited:
        module.main(args)
    assert exited.value.code == 2
    output = capsys.readouterr()
    assert output.out == ""
    assert "local_reinstall_disabled" in output.err
    assert "not reinstall/restore" in output.err
    assert "online-authorized-management-target" in output.err
    assert "No generic recovery Job" in output.err
    assert "SYNTHETIC_KUBECONFIG" not in output.err
    assert os.environ["KUBECONFIG"] == "unchanged-inspection-config"
    assert not backup_dir.exists()


def test_no_local_mutation_helpers_remain_importable(module):
    for name in ("reinstall", "restore", "pull_check", "pullcheck_manifest", "write_backup", "delete_old"):
        assert not hasattr(module, name)


@pytest.mark.parametrize("args,error", [
    (["plan"], "plan requires --cluster and --instance"),
    (["plan", "--cluster", "c", "--instance", "i", "--backup-dir", "/unused"], "--backup-dir is retired"),
])
def test_plan_requires_identity_and_rejects_backup_writes(module, monkeypatch, capsys, args, error):
    monkeypatch.setattr(module, "plan", lambda *a: pytest.fail("invalid planning arguments reached inspection"))
    with pytest.raises(SystemExit) as exited:
        module.main(args)
    assert exited.value.code == 2
    assert error in capsys.readouterr().err


# --- read-only image resolution ----------------------------------------------

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
            assert "scope=repository%3Adoops-agent%3Apull" in request
            return Response(token)
        assert request.get_method() == "HEAD"
        if "Authorization" not in request.headers:
            headers = Message()
            headers["WWW-Authenticate"] = 'Bearer realm="https://registry.example.com/service/token",service="doops-registry"'
            raise urllib.error.HTTPError(request.full_url, 401, "unauthorized", headers, None)
        return Response(headers={"Docker-Content-Digest": digest})

    return urlopen, calls


@pytest.mark.parametrize("image,want", [
    ("registry.example.com/doops-agent:latest", "registry.example.com/doops-agent@sha256:" + "f" * 64),
    ("registry.example.com/doops-agent", "registry.example.com/doops-agent@sha256:" + "f" * 64),
    (IMAGE, IMAGE),
])
def test_resolve_image_pins_tags_to_the_registry_digest(image, want):
    urlopen, _calls = registry()
    assert load().resolve_image(image, urlopen=urlopen) == want


@pytest.mark.parametrize("image,urlopen,error", [
    ("doops.sh:latest", registry()[0], "registry host"),
    ("registry.example.com/doops-agent:latest", registry(digest="")[0], "pinned"),
    ("registry.example.com/doops-agent:latest", registry(token=b"{}")[0], "no token"),
])
def test_resolve_image_rejects_unpinnable_references(image, urlopen, error):
    with pytest.raises(load().ReinstallError, match=error):
        load().resolve_image(image, urlopen=urlopen)


@pytest.fixture
def cli(tmp_path):
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    calls_file = tmp_path / "calls.jsonl"
    state = tmp_path / "cluster.json"
    cluster = FakeCluster(releases=["doops-agent"])
    deployment = cluster.objects["deployment"]["doops-agent"]
    deployment["spec"]["template"]["spec"]["containers"][0]["env"].append(
        {"name": "LEGACY_SECRET_CANARY", "value": SETTINGS_CANARY})
    state.write_text(json.dumps({"objects": cluster.objects, "lists": cluster.lists}))
    tool = f"#!{sys.executable}\n" + """
import json
import os
import sys
from pathlib import Path

name, args = Path(sys.argv[0]).name, sys.argv[1:]
with open(os.environ["REINSTALL_TEST_CALLS"], "a") as stream:
    stream.write(json.dumps([name, *args]) + "\\n")
state = json.loads(Path(os.environ["REINSTALL_TEST_STATE"]).read_text())
if name == "helm" and args[:1] == ["list"]:
    print(json.dumps([{"name": "doops-agent"}]))
elif name == "kubectl" and args[2:3] == ["get"]:
    kind = args[3]
    if kind in state["lists"]:
        print(json.dumps({"items": state["lists"][kind]}))
    else:
        obj = state["objects"][kind].get(args[4])
        if obj is not None:
            print(json.dumps(obj))
        elif "--ignore-not-found" not in args:
            raise SystemExit(1)
else:
    raise SystemExit("unexpected controller command")
"""
    for name in ("kubectl", "helm", "doops", "docker", "ssh"):
        executable = bin_dir / name
        executable.write_text(tool)
        executable.chmod(0o755)

    def run(action, extra=()):
        env = {
            **os.environ, "PATH": str(bin_dir), "HOME": str(tmp_path),
            "PYTHONDONTWRITEBYTECODE": "1", "REINSTALL_TEST_CALLS": str(calls_file),
            "REINSTALL_TEST_STATE": str(state),
        }
        result = subprocess.run(
            [sys.executable, str(SCRIPT), action, "--cluster", "doops-demo", "--instance", "demo-228",
             "--image", IMAGE, *extra],
            cwd=ROOT, env=env, capture_output=True, text=True, timeout=10, check=False,
        )
        calls = [json.loads(line) for line in calls_file.read_text().splitlines()] if calls_file.exists() else []
        return result, calls

    return run


@pytest.mark.parametrize("action", ["run", "restore"])
def test_retired_cli_never_contacts_cluster_or_invokes_upgrade(cli, action):
    result, calls = cli(action)
    assert result.returncode == 2
    assert "local_reinstall_disabled" in result.stderr
    assert result.stdout == ""
    assert calls == []


def test_real_planning_cli_reads_only_metadata_and_never_creates_backup(cli, tmp_path):
    result, calls = cli("plan", ["--kubeconfig", str(tmp_path / "read-only-config")])
    assert result.returncode == 0, result.stderr
    output = json.loads(result.stdout)
    assert output["phase"] == "plan"
    assert output["readOnly"] is True and output["mutationSupported"] is False
    assert output["image"] == IMAGE and output["inventory"]["helmRelease"] == "doops-agent"
    assert output["source"] == "deployment/doops-agent"
    assert SETTINGS_CANARY not in result.stdout + result.stderr
    assert calls
    assert all(c[:2] == ["helm", "list"] or (c[0] == "kubectl" and c[3] == "get") for c in calls)
    assert not any("secret" in c or "doops-agent-settings" in c for c in calls)
    assert not list(tmp_path.glob("doops-agent-backup-*"))
