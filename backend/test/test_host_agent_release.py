import copy
import importlib.util
import json
import os
import shutil
import stat
import subprocess
import sys
from pathlib import Path
from types import SimpleNamespace
from urllib.parse import parse_qs, unquote, urlsplit

import pytest


ROOT = Path(__file__).resolve().parents[1]
CONTROLLER = ROOT / "scripts" / "release-host-agent.py"
SHELL = ROOT / "scripts" / "release-host-agent.sh"
CANARY = "host-release-synthetic-env-secret-7b41"
IMAGE = "registry.example.invalid/doops-agent@sha256:" + "a" * 64
SOURCE = "b" * 40
VERSION = "v1.2.3"
OLD_ID = "1" * 64
NEW_ID = "2" * 64
IMAGE_ID = "sha256:" + "c" * 64


@pytest.fixture
def args():
    return SimpleNamespace(
        container="doops-agent",
        session="host-release-test",
        cluster="test-cluster",
        instance="test-node",
        image=IMAGE,
        source_commit=SOURCE,
        release=VERSION,
        controller_id="host-controller-test",
        dry_run=False,
        rollback=False,
    )


@pytest.fixture
def controller(monkeypatch, tmp_path):
    spec = importlib.util.spec_from_file_location("host_agent_release", CONTROLLER)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    handlers = {number: module.signal.getsignal(number)
                for number in (module.signal.SIGTERM, module.signal.SIGHUP)}
    module.STATE_ROOT = tmp_path / "receipts"
    module.LOCK_ROOT = tmp_path / "locks"
    module.LOCK_ROOT.mkdir()
    monkeypatch.setattr(module.os, "geteuid", lambda: 0)
    monkeypatch.delenv("KUBERNETES_SERVICE_HOST", raising=False)
    real_exists = Path.exists
    monkeypatch.setattr(
        module.Path, "exists",
        lambda path: False if str(path) == "/.dockerenv" else real_exists(path),
    )
    now = [0.0]
    monkeypatch.setattr(module.time, "monotonic", lambda: now[0])

    def sleep(seconds):
        now[0] += seconds

    monkeypatch.setattr(module.time, "sleep", sleep)
    yield module
    for number, handler in handlers.items():
        module.signal.signal(number, handler)


def receipt_path(controller, args):
    return controller.STATE_ROOT / args.container / args.session / "state.json"


class FakeDocker:
    """Stateful Docker/CLI boundaries; the production transaction remains intact."""

    def __init__(self, controller, args, settings_source):
        self.controller = controller
        self.args = args
        self.events = []
        self.fault = None
        self.created_body = None
        self.at_stop = None
        self.old_running_at_new_start = None
        self.binary_version = "release=%s commit=%s" % (VERSION, SOURCE)
        self.image_info = {
            "Id": IMAGE_ID,
            "RepoDigests": [IMAGE],
            "Config": {"Labels": {
                "org.opencontainers.image.revision": SOURCE,
                "org.opencontainers.image.version": VERSION,
                "org.opencontainers.image.source": "https://example.invalid/doops",
            }},
        }
        original = {
            "Id": OLD_ID,
            "Name": "/" + args.container,
            "Image": "sha256:" + "d" * 64,
            "State": {"Running": True},
            "Config": {
                "Image": "registry.example.invalid/doops-agent:v1.2.2",
                "Entrypoint": ["/app/doops-agent"],
                "Cmd": ["-cluster", args.cluster, "-instance", args.instance,
                        "-port", "43123", "-log-level", "info"],
                "Env": ["DOOPS_GATEWAY_CLUSTER=" + args.cluster,
                        "DOOPS_GATEWAY_INSTANCE=" + args.instance,
                        "DOOPS_TEST_SECRET=" + CANARY, "TZ=UTC"],
                "Labels": {
                    "org.opencontainers.image.revision": "e" * 40,
                    "org.opencontainers.image.version": "v1.2.2",
                    "operator.example/owner": "existing-owner",
                },
                "WorkingDir": "/app",
                "User": "0",
                "StopSignal": "SIGTERM",
                "Tty": False,
                "OpenStdin": False,
                "ExposedPorts": {"43123/tcp": {}},
            },
            "HostConfig": {
                "Binds": ["/srv/doops:/root/.doops:rw", "/srv/kube:/root/.kube:ro",
                          str(settings_source) + ":/opt/doagent_config/settings.json:ro"],
                "NetworkMode": "host",
                "Privileged": True,
                "RestartPolicy": {"Name": "on-failure", "MaximumRetryCount": 7},
                "AutoRemove": False,
                "SecurityOpt": ["label=disable"],
                "Dns": ["192.0.2.53"],
                "LogConfig": {"Type": "json-file", "Config": {"max-size": "10m"}},
            },
            "Mounts": [{"Destination": "/root/.doops"}, {"Destination": "/root/.kube"},
                       {"Destination": "/opt/doagent_config/settings.json",
                        "Source": str(settings_source), "RW": False}],
        }
        self.containers = {OLD_ID: original}
        self.original = copy.deepcopy(original)

    @property
    def old(self):
        return self.containers[OLD_ID]

    @property
    def mutations(self):
        return [event for event in self.events
                if event[0] == "api" and event[1] != "GET"]

    def api(self, method, path, body=None):
        self.events.append(("api", method, path))
        parsed = urlsplit(path)
        if method == "GET" and parsed.path.startswith("/images/"):
            assert unquote(parsed.path[len("/images/"):-len("/json")]) == IMAGE
            if self.fault == "missing_image":
                raise RuntimeError("docker_api_failed: GET /images/json HTTP 404")
            return copy.deepcopy(self.image_info)
        if method == "POST" and parsed.path == "/containers/create":
            self.created_body = copy.deepcopy(body)
            if self.fault == "create":
                raise RuntimeError("docker_api_failed: POST /containers/create HTTP 500")
            name = parse_qs(parsed.query)["name"][0]
            config = copy.deepcopy(body)
            host_config = config.pop("HostConfig")
            self.containers[NEW_ID] = {
                "Id": NEW_ID, "Name": "/" + name, "Image": IMAGE_ID,
                "Config": config, "HostConfig": host_config, "State": {"Running": False},
            }
            return {"Id": NEW_ID, "Warnings": []}
        parts = parsed.path.strip("/").split("/")
        assert len(parts) >= 2 and parts[0] == "containers", (method, path)
        reference = unquote(parts[1])
        container = next((item for item in self.containers.values()
                          if reference in (item["Id"], item["Name"].lstrip("/"))), None)
        if container is None:
            raise self.controller.DockerAPIError(method, path, 404)
        if method == "GET" and parts[2:] == ["json"]:
            return copy.deepcopy(container)
        if method == "DELETE":
            assert parse_qs(parsed.query) == {"force": ["1"]}
            del self.containers[container["Id"]]
            return None
        assert method == "POST", (method, path)
        action = parts[2]
        if action == "stop":
            self.at_stop = {
                "new_running": self.containers[NEW_ID]["State"]["Running"],
                "new_name": self.containers[NEW_ID]["Name"],
                "receipt": json.loads(receipt_path(self.controller, self.args).read_text()),
            }
            container["State"]["Running"] = False
        elif action == "rename":
            if container["Id"] == OLD_ID and self.fault == "rollback_rename":
                raise self.controller.DockerAPIError(method, path, 500)
            name = "/" + parse_qs(parsed.query)["name"][0]
            assert not any(item["Id"] != container["Id"] and item["Name"] == name
                           for item in self.containers.values())
            container["Name"] = name
        elif action == "start":
            if container["Id"] == OLD_ID and self.fault == "rollback_start":
                raise self.controller.DockerAPIError(method, path, 500)
            if container["Id"] == NEW_ID:
                self.old_running_at_new_start = self.old["State"]["Running"]
                if self.fault == "startup":
                    raise RuntimeError("docker_api_failed: POST /containers/start HTTP 500")
            container["State"]["Running"] = True
        else:
            pytest.fail("Unexpected Docker mutation: " + path)

    def cli(self, *command):
        self.events.append(("cli", *command))
        assert command[0] == "docker", command
        if command[1] == "pull":
            assert command == ("docker", "pull", "--platform", "linux/amd64", IMAGE)
            return ""
        if command[1] == "run":
            assert command == ("docker", "run", "--rm", "--network", "none",
                               "--entrypoint", "/app/doops-agent", IMAGE, "-version")
            return self.binary_version
        assert command[1] == "exec" and command[2] == NEW_ID, command
        assert self.containers[NEW_ID]["State"]["Running"]
        if command[3:] == ("/app/doops-agent", "-version"):
            return self.binary_version
        assert command[3:] == ("curl", "-fsS", "--max-time", "2",
                               "http://127.0.0.1:43123/health/ready"), command
        if self.fault == "ready":
            raise RuntimeError("release_command_failed: docker")
        return '{"ready":true}'


@pytest.fixture
def docker(controller, args, monkeypatch, tmp_path):
    settings_source = tmp_path / "settings.json"
    settings_source.write_text(json.dumps({"synthetic_canary": CANARY}))
    fake = FakeDocker(controller, args, settings_source)
    monkeypatch.setattr(controller, "docker_api", fake.api)
    monkeypatch.setattr(controller, "cli", fake.cli)
    return fake


def assert_private_receipts(controller, captured):
    assert CANARY not in captured.out + captured.err
    for path in controller.STATE_ROOT.rglob("*"):
        if path.is_file():
            text = path.read_text()
            assert CANARY not in text
            if path.name == "state.json":
                state = json.loads(text)
                assert not {"Config", "HostConfig", "Env", "Cmd"}.intersection(state)


@pytest.mark.parametrize("mismatch", ["cluster", "instance", "source", "version", "binary"])
def test_preflight_mismatch_fails_before_replacement_or_old_stop(
    controller, args, docker, capsys, mismatch,
):
    if mismatch in ("cluster", "instance"):
        index = docker.old["Config"]["Cmd"].index("-" + mismatch) + 1
        docker.old["Config"]["Cmd"][index] = "another-agent"
        error = "agent_identity_mismatch"
    elif mismatch in ("source", "version"):
        label = "revision" if mismatch == "source" else "version"
        docker.image_info["Config"]["Labels"]["org.opencontainers.image." + label] = "wrong"
        error = "image_provenance_mismatch"
    else:
        docker.binary_version = "release=v1.2.2 commit=" + "e" * 40
        error = "image_binary_provenance_mismatch"
    with pytest.raises(RuntimeError, match=error):
        controller.release(args)
    assert not docker.mutations
    assert docker.old["State"]["Running"]
    assert docker.old["Name"] == "/" + args.container
    assert json.loads(receipt_path(controller, args).read_text())["phase"] == "preflight"
    assert_private_receipts(controller, capsys.readouterr())


@pytest.mark.parametrize("inside", ["docker", "kubernetes"])
def test_containerized_controller_fails_before_any_docker_call(
    controller, args, docker, monkeypatch, inside,
):
    if inside == "docker":
        real_exists = Path.exists
        monkeypatch.setattr(controller.Path, "exists",
                            lambda path: True if str(path) == "/.dockerenv" else real_exists(path))
    else:
        monkeypatch.setenv("KUBERNETES_SERVICE_HOST", "192.0.2.1")
    with pytest.raises(RuntimeError, match="external_host_controller_required"):
        controller.release(args)
    assert not docker.events
    assert docker.old["State"]["Running"]


def test_agent_without_settings_mount_passes_preflight(
    controller, args, docker, capsys,
):
    # The model provider comes from the Gateway; no settings mount is required.
    docker.old["Mounts"] = [
        mount for mount in docker.old["Mounts"]
        if not mount["Destination"].startswith("/opt/doagent_config")
    ]
    args.dry_run = True
    controller.release(args)
    assert all(event[:2] == ("api", "GET") for event in docker.events)
    assert "preflight_ready" in capsys.readouterr().out


def test_dry_run_rollback_rejected_before_uid_lock_remote_or_receipt(
    controller, args, monkeypatch, capsys,
):
    args.dry_run = args.rollback = True
    calls = []

    def unexpected_boundary(*_args, **_kwargs):
        calls.append(True)
        pytest.fail("Dry-run rollback reached a host or mutation boundary")

    monkeypatch.setattr(controller.os, "geteuid", unexpected_boundary)
    monkeypatch.setattr(controller, "open", unexpected_boundary, raising=False)
    monkeypatch.setattr(controller.fcntl, "flock", unexpected_boundary)
    for name in ("docker_api", "cli", "restore", "save_state"):
        monkeypatch.setattr(controller, name, unexpected_boundary)
    with pytest.raises(RuntimeError, match="^dry_run_cannot_mutate_rollback$"):
        controller.release(args)
    assert not calls
    assert not list(controller.LOCK_ROOT.iterdir())
    assert not controller.STATE_ROOT.exists()
    captured = capsys.readouterr()
    assert not captured.out and not captured.err


def test_dry_run_only_inspects_preloaded_image_and_original_config(
    controller, args, docker, capsys,
):
    args.dry_run = True
    controller.release(args)
    assert len(docker.events) == 2
    assert all(event[:2] == ("api", "GET") for event in docker.events)
    assert docker.containers == {OLD_ID: docker.original}
    assert docker.created_body is None
    assert not receipt_path(controller, args).exists()
    captured = capsys.readouterr()
    assert "preflight_ready" in captured.out
    assert "host_upgrade_ready" not in captured.out
    assert_private_receipts(controller, captured)


@pytest.mark.parametrize("fault", ["missing_image", "source", "version"])
def test_dry_run_missing_or_wrong_preloaded_artifact_fails_without_pull_or_version_run(
    controller, args, docker, capsys, fault,
):
    args.dry_run = True
    if fault == "missing_image":
        docker.fault = fault
        error = "docker_api_failed"
    else:
        label = "revision" if fault == "source" else "version"
        docker.image_info["Config"]["Labels"]["org.opencontainers.image." + label] = "wrong"
        error = "image_provenance_mismatch"
    with pytest.raises(RuntimeError, match=error):
        controller.release(args)
    assert all(event[:2] == ("api", "GET") for event in docker.events)
    assert docker.containers == {OLD_ID: docker.original}
    assert not receipt_path(controller, args).exists()
    captured = capsys.readouterr()
    assert "preflight_ready" not in captured.out
    assert_private_receipts(controller, captured)


def test_transaction_preserves_opaque_config_retains_old_and_records_new_ready(
    controller, args, docker, capsys,
):
    controller.release(args)
    state_path = receipt_path(controller, args)
    state = json.loads(state_path.read_text())
    expected = copy.deepcopy(docker.original["Config"])
    expected["Image"] = IMAGE
    expected["Labels"].update(docker.image_info["Config"]["Labels"])
    expected["HostConfig"] = docker.original["HostConfig"]
    assert docker.created_body == expected
    assert CANARY in "\n".join(docker.created_body["Env"])
    assert docker.original["Config"]["Labels"]["org.opencontainers.image.version"] == "v1.2.2"
    assert docker.old["Config"] == docker.original["Config"]
    assert docker.old["HostConfig"] == docker.original["HostConfig"]
    assert docker.at_stop["new_running"] is False
    assert docker.at_stop["new_name"] == "/" + args.container + "-replacement-" + args.session
    assert docker.at_stop["receipt"]["new_id"] == NEW_ID
    assert docker.at_stop["receipt"]["old_id"] == OLD_ID
    assert docker.at_stop["receipt"]["phase"] == "replacing"
    assert docker.mutations == [
        ("api", "POST", "/containers/create?name=" + args.container + "-replacement-" + args.session),
        ("api", "POST", "/containers/%s/stop?t=30" % OLD_ID),
        ("api", "POST", "/containers/%s/rename?name=%s-rollback-%s" % (OLD_ID, args.container, args.session)),
        ("api", "POST", "/containers/%s/rename?name=%s" % (NEW_ID, args.container)),
        ("api", "POST", "/containers/%s/start" % NEW_ID),
    ]
    pull = next(index for index, event in enumerate(docker.events)
                if event[:3] == ("cli", "docker", "pull"))
    version_run = next(index for index, event in enumerate(docker.events)
                       if event[:3] == ("cli", "docker", "run"))
    create = docker.events.index(docker.mutations[0])
    stop = docker.events.index(docker.mutations[1])
    assert pull < version_run < create < stop
    assert docker.old_running_at_new_start is False
    assert docker.old["Name"] == "/" + args.container + "-rollback-" + args.session
    assert docker.old["State"]["Running"] is False
    assert docker.containers[NEW_ID]["Name"] == "/" + args.container
    assert docker.containers[NEW_ID]["State"]["Running"] is True
    assert state["phase"] == "host_ready"
    assert state["new_id"] == NEW_ID and state["old_id"] == OLD_ID
    assert state["image"] == IMAGE and state["source_commit"] == SOURCE
    assert stat.S_IMODE(state_path.stat().st_mode) == 0o600
    assert stat.S_IMODE(state_path.parent.stat().st_mode) == 0o700
    assert {path.name for path in state_path.parent.iterdir()} == {"state.json"}
    assert not any(event[1] == "DELETE" for event in docker.mutations)
    captured = capsys.readouterr()
    assert "host_upgrade_ready" in captured.out and NEW_ID in captured.out
    assert "host_rollback_restored" not in captured.err
    assert_private_receipts(controller, captured)


def test_create_failure_never_stops_old(controller, args, docker, capsys):
    docker.fault = "create"
    with pytest.raises(RuntimeError, match="docker_api_failed"):
        controller.release(args)
    assert docker.containers == {OLD_ID: docker.original}
    assert all(urlsplit(event[2]).path == "/containers/create" for event in docker.mutations)
    assert json.loads(receipt_path(controller, args).read_text())["phase"] == "rolled_back"
    captured = capsys.readouterr()
    assert "host_upgrade_ready" not in captured.out
    assert_private_receipts(controller, captured)


@pytest.mark.parametrize("fault,error", [
    ("startup", "docker_api_failed"),
    ("ready", "gateway_handoff_or_functional_probe_failed"),
])
def test_failed_replacement_restores_original_id_and_never_marks_success(
    controller, args, docker, capsys, fault, error,
):
    docker.fault = fault
    with pytest.raises(RuntimeError, match=error):
        controller.release(args)
    assert docker.containers == {OLD_ID: docker.original}
    state = json.loads(receipt_path(controller, args).read_text())
    assert state["phase"] == "rolled_back" and state["old_id"] == OLD_ID
    assert any(event == ("api", "POST", "/containers/%s/start" % OLD_ID)
               for event in docker.events)
    assert not any(event[1] == "DELETE" and OLD_ID in event[2] for event in docker.mutations)
    captured = capsys.readouterr()
    assert "host_upgrade_ready" not in captured.out
    assert "host_rollback_restored old_id=" + OLD_ID in captured.err
    assert_private_receipts(controller, captured)


def python_argv(args):
    result = [str(CONTROLLER)]
    for name in ("container", "session", "cluster", "instance", "image", "source_commit", "release", "controller_id"):
        result.extend(["--" + name.replace("_", "-"), getattr(args, name)])
    return result


@pytest.mark.parametrize("fault", ["rollback_rename", "rollback_start"])
def test_partial_rollback_retries_exact_ids_after_new_delete(
    controller, args, docker, monkeypatch, capsys, fault,
):
    controller.release(args)
    capsys.readouterr()
    docker.fault = fault
    monkeypatch.setattr(sys, "argv", python_argv(args) + ["--rollback"])
    assert controller.main() == 1
    assert NEW_ID not in docker.containers
    assert not docker.old["State"]["Running"]
    state = json.loads(receipt_path(controller, args).read_text())
    assert state["old_id"] == OLD_ID and state["new_id"] == NEW_ID
    assert state["new_removal_started"] is True and state["phase"] != "rolled_back"
    captured = capsys.readouterr()
    assert "HTTP 500" in captured.err
    assert "host_rollback_restored" not in captured.out
    docker.fault = None
    docker.events.clear()
    assert controller.main() == 0
    assert docker.containers == {OLD_ID: docker.original}
    assert not any(event[1] == "DELETE" for event in docker.mutations)
    assert all(OLD_ID in event[2] for event in docker.mutations)
    assert json.loads(receipt_path(controller, args).read_text())["phase"] == "rolled_back"
    assert_private_receipts(controller, capsys.readouterr())
    docker.events.clear()
    assert controller.main() == 0
    assert not docker.mutations


@pytest.mark.parametrize("change", ["name", "same_name_different_id"])
def test_partial_rollback_retry_refuses_changed_old_owner(
    controller, args, docker, monkeypatch, capsys, change,
):
    controller.release(args)
    docker.fault = "rollback_rename"
    monkeypatch.setattr(sys, "argv", python_argv(args) + ["--rollback"])
    assert controller.main() == 1
    capsys.readouterr()
    docker.fault = None
    if change == "name":
        docker.old["Name"] = "/owned-by-another-release"
        error = "old_container_ownership_changed"
    else:
        foreign = docker.containers.pop(OLD_ID)
        foreign["Id"] = "9" * 64
        docker.containers[foreign["Id"]] = foreign
        error = "HTTP 404"
    before = copy.deepcopy(docker.containers)
    receipt_before = receipt_path(controller, args).read_text()
    docker.events.clear()
    assert controller.main() == 1
    assert error in capsys.readouterr().err
    assert not docker.mutations and docker.containers == before
    assert receipt_path(controller, args).read_text() == receipt_before


@pytest.mark.parametrize("status", [403, 404, 500])
def test_rollback_does_not_ignore_unproven_absence_or_other_inspect_errors(
    controller, args, docker, monkeypatch, status,
):
    controller.release(args)
    state_path = receipt_path(controller, args)
    if status != 404:
        state = json.loads(state_path.read_text())
        state["new_removal_started"] = True
        controller.save_state(state_path.parent, state)
    receipt_before = state_path.read_text()
    before = copy.deepcopy(docker.containers)
    original_api = docker.api

    def failed_inspect(method, path, body=None):
        if method == "GET" and path == "/containers/%s/json" % NEW_ID:
            raise controller.DockerAPIError(method, path, status)
        return original_api(method, path, body)

    monkeypatch.setattr(controller, "docker_api", failed_inspect)
    args.rollback = True
    docker.events.clear()
    with pytest.raises(controller.DockerAPIError) as error:
        controller.release(args)
    assert error.value.status == status
    assert not docker.mutations and docker.containers == before
    assert state_path.read_text() == receipt_before


@pytest.mark.parametrize("status", [403, 404, 500])
def test_rollback_delete_errors_remain_failures_without_restoring_old(
    controller, args, docker, monkeypatch, status,
):
    controller.release(args)
    original_api = docker.api

    def failed_delete(method, path, body=None):
        if method == "DELETE":
            raise controller.DockerAPIError(method, path, status)
        return original_api(method, path, body)

    monkeypatch.setattr(controller, "docker_api", failed_delete)
    args.rollback = True
    before = copy.deepcopy(docker.containers)
    docker.events.clear()
    with pytest.raises(controller.DockerAPIError) as error:
        controller.release(args)
    assert error.value.status == status
    assert not docker.mutations and docker.containers == before
    state = json.loads(receipt_path(controller, args).read_text())
    assert state["new_removal_started"] is True and state["phase"] != "rolled_back"


def test_rollback_persists_removal_intent_before_any_delete(
    controller, args, docker, monkeypatch,
):
    controller.release(args)
    receipt_before = receipt_path(controller, args).read_text()

    def failed_save(*_args):
        raise OSError("synthetic_removal_intent_write_failure")

    monkeypatch.setattr(controller, "save_state", failed_save)
    args.rollback = True
    before = copy.deepcopy(docker.containers)
    docker.events.clear()
    with pytest.raises(OSError, match="synthetic_removal_intent_write_failure"):
        controller.release(args)
    assert not docker.mutations and docker.containers == before
    assert receipt_path(controller, args).read_text() == receipt_before


def test_rollback_never_deletes_old_id_as_replacement(controller, args, docker):
    controller.release(args)
    state_path = receipt_path(controller, args)
    state = json.loads(state_path.read_text())
    state["new_id"] = OLD_ID
    controller.save_state(state_path.parent, state)
    args.rollback = True
    docker.events.clear()
    with pytest.raises(RuntimeError, match="replacement_identity_matches_old"):
        controller.release(args)
    assert not docker.events


@pytest.mark.parametrize("changed_id", [OLD_ID, NEW_ID])
def test_rollback_refuses_inspect_id_mismatch_before_mutation(
    controller, args, docker, monkeypatch, changed_id,
):
    controller.release(args)
    original_api = docker.api

    def changed_inspect(method, path, body=None):
        result = original_api(method, path, body)
        if method == "GET" and path == "/containers/%s/json" % changed_id:
            result["Id"] = "9" * 64
        return result

    monkeypatch.setattr(controller, "docker_api", changed_inspect)
    args.rollback = True
    docker.events.clear()
    receipt_before = receipt_path(controller, args).read_text()
    with pytest.raises(RuntimeError, match="ownership_changed"):
        controller.release(args)
    assert not docker.mutations
    assert receipt_path(controller, args).read_text() == receipt_before


@pytest.mark.parametrize("failure", ["inspect", "binary", "prepared_receipt"])
def test_preflight_receipt_is_the_only_noop_evidence(
    controller, args, docker, monkeypatch, capsys, failure,
):
    if failure == "inspect":
        def failed_inspect(*_args):
            assert json.loads(receipt_path(controller, args).read_text())["phase"] == "preflight"
            raise RuntimeError("synthetic_preflight_inspect_failure")
        monkeypatch.setattr(controller, "docker_api", failed_inspect)
        error = RuntimeError
    elif failure == "binary":
        docker.binary_version = "wrong"
        error = RuntimeError
    else:
        real_save = controller.save_state

        def failed_save(directory, state):
            if state["phase"] == "prepared":
                raise OSError("synthetic_prepared_receipt_write_failure")
            real_save(directory, state)
        monkeypatch.setattr(controller, "save_state", failed_save)
        error = OSError
    with pytest.raises(error):
        controller.release(args)
    state_path = receipt_path(controller, args)
    receipt_before = state_path.read_text()
    assert json.loads(receipt_before) == {"phase": "preflight", "controller_id": args.controller_id,
                                         "image": IMAGE, "source_commit": SOURCE}
    assert {path.name for path in state_path.parent.iterdir()} == {"state.json"}
    assert not docker.mutations
    docker.events.clear()
    monkeypatch.setattr(sys, "argv", python_argv(args) + ["--rollback"])
    assert controller.main() == 0
    assert not docker.events and docker.containers == {OLD_ID: docker.original}
    assert state_path.read_text() == receipt_before
    captured = capsys.readouterr()
    assert "host_rollback_not_started" in captured.out and "phase=preflight" in captured.out
    assert "host_rollback_restored" not in captured.out
    assert_private_receipts(controller, captured)


@pytest.mark.parametrize("missing", ["receipt", "directory", "first_receipt", "never_invoked"])
def test_rollback_without_receipt_and_without_noop_proof_fails_closed(
    controller, args, docker, monkeypatch, capsys, missing,
):
    if missing == "first_receipt":
        def failed_save(*_args):
            raise OSError("synthetic_first_receipt_write_failure")
        monkeypatch.setattr(controller, "save_state", failed_save)
        with pytest.raises(OSError, match="synthetic_first_receipt_write_failure"):
            controller.release(args)
        assert not docker.events and not receipt_path(controller, args).exists()
    elif missing != "never_invoked":
        controller.release(args)
        if missing == "receipt":
            receipt_path(controller, args).unlink()
        else:
            shutil.rmtree(receipt_path(controller, args).parent)
    capsys.readouterr()
    before = copy.deepcopy(docker.containers)
    docker.events.clear()
    monkeypatch.setattr(sys, "argv", python_argv(args) + ["--rollback"])
    assert controller.main() == 1
    assert not docker.events and docker.containers == before
    captured = capsys.readouterr()
    assert "rollback_receipt_missing" in captured.err
    assert "host_rollback_not_started" not in captured.out
    assert "host_rollback_restored" not in captured.out


def test_duplicate_session_never_rolls_back_previous_attempt(
    controller, args, docker, capsys,
):
    controller.release(args)
    capsys.readouterr()
    before = copy.deepcopy(docker.containers)
    receipt_before = receipt_path(controller, args).read_text()
    args.controller_id = "different-host-controller"
    docker.events.clear()
    with pytest.raises(FileExistsError):
        controller.release(args)
    args.rollback = True
    with pytest.raises(RuntimeError, match="rollback_controller_mismatch"):
        controller.release(args)
    assert not docker.events and docker.containers == before
    assert receipt_path(controller, args).read_text() == receipt_before


@pytest.mark.parametrize("changed_id", [NEW_ID, OLD_ID], ids=["replacement-owner", "old-owner"])
def test_external_rollback_refuses_ownership_change_before_any_mutation(
    controller, args, docker, monkeypatch, capsys, changed_id,
):
    controller.release(args)
    capsys.readouterr()
    docker.containers[changed_id]["Name"] = "/owned-by-another-release"
    before = copy.deepcopy(docker.containers)
    receipt_before = receipt_path(controller, args).read_text()
    docker.events.clear()
    monkeypatch.setattr(sys, "argv", python_argv(args) + ["--rollback"])
    assert controller.main() == 1
    assert not docker.mutations
    assert docker.containers == before
    assert receipt_path(controller, args).read_text() == receipt_before
    captured = capsys.readouterr()
    assert "ownership_changed" in captured.err
    assert "host_rollback_restored" not in captured.out
    assert_private_receipts(controller, captured)


def test_docker_error_body_with_canary_is_never_exposed(controller, monkeypatch, capsys):
    requests = []

    class Connection:
        def __init__(self, *_args, **_kwargs):
            pass

        def request(self, method, path, payload, headers):
            requests.append((method, path, payload, headers))

        def getresponse(self):
            return SimpleNamespace(status=500, read=lambda: CANARY.encode())

        def close(self):
            pass

    monkeypatch.setattr(controller, "DockerConnection", Connection)
    with pytest.raises(RuntimeError) as error:
        controller.docker_api("POST", "/containers/create?name=replacement",
                              {"Env": ["TEST_SECRET=" + CANARY]})
    assert CANARY.encode() in requests[0][2]
    assert "HTTP 500" in str(error.value)
    assert CANARY not in str(error.value)
    captured = capsys.readouterr()
    assert CANARY not in captured.out + captured.err


def test_cli_failure_redacts_both_output_streams(controller, monkeypatch, capsys):
    def failed_command(command, **_kwargs):
        return subprocess.CompletedProcess(command, 1, CANARY.encode(), CANARY.encode())

    monkeypatch.setattr(controller.subprocess, "run", failed_command)
    with pytest.raises(RuntimeError) as error:
        controller.cli("docker", "pull", IMAGE)
    assert str(error.value) == "release_command_failed: docker"
    captured = capsys.readouterr()
    assert CANARY not in captured.out + captured.err


@pytest.fixture
def shell_runner(tmp_path):
    calls_path = tmp_path / "calls.log"
    bash_env = tmp_path / "shell-boundaries.sh"
    bash_env.write_text(r'''
doops() {
  printf 'doops %s\n' "$*" >> "$HOST_RELEASE_CALLS"
  if [[ "$*" == list ]]; then
    printf '%s\n' 'host-target gateway test-cluster/test-node'
  elif [[ "$*" == '-session host-release-test targets --target host-target' ]]; then
    printf 'test-cluster test-node online %s\n' "$HOST_RELEASE_SOURCE"
  elif [[ "$*" == '-session host-release-test capabilities --target host-target --require structuredSSH' ]]; then
    printf '%s\n' 'Legacy RPC does not support structuredSSH' >&2
    return 1
  else
    return 99
  fi
}
ssh() {
  printf 'ssh %s\n' "$*" >> "$HOST_RELEASE_CALLS"
  case "${!#}" in
    'mktemp '*) printf '%s\n' '/tmp/doops-host-release.synthetic' ;;
    *'--rollback'*) printf '%s\n' 'host_rollback_restored' ;;
    *'$executor '*) printf '%s\n' 'host_upgrade_ready' ;;
    'test '*|'rm -f '*) return 0 ;;
    *) return 99 ;;
  esac
}
scp() { printf 'scp %s\n' "$*" >> "$HOST_RELEASE_CALLS"; }
sha256sum() { printf '%s %s\n' "$HOST_RELEASE_HASH" "$1"; }
sleep() { SECONDS=$((SECONDS + 121)); }
''')
    env = {
        "PATH": os.environ.get("PATH", ""),
        "HOME": str(tmp_path),
        "LC_ALL": "C",
        "BASH_ENV": str(bash_env),
        "HOST_RELEASE_CALLS": str(calls_path),
        "HOST_RELEASE_SOURCE": SOURCE,
        "HOST_RELEASE_HASH": "f" * 64,
    }
    command = [shutil.which("bash"), str(SHELL),
               "--target", "host-target", "--session", "host-release-test",
               "--host", "host.example.invalid", "--user", "operator", "--port", "22022",
               "--container", "doops-agent", "--cluster", "test-cluster",
               "--instance", "test-node", "--image", IMAGE, "--source-commit", SOURCE,
               "--release", VERSION]

    def run(*, missing_port=False):
        argv = list(command)
        if missing_port:
            index = argv.index("--port")
            del argv[index:index + 2]
        result = subprocess.run(argv, cwd=ROOT, env=env, text=True,
                                capture_output=True, timeout=10, check=False)
        calls = calls_path.read_text() if calls_path.exists() else ""
        return result, calls

    return run


def test_shell_missing_explicit_port_fails_before_any_remote_call(shell_runner):
    result, calls = shell_runner(missing_port=True)
    assert result.returncode == 2
    assert "Explicit SSH --port required" in result.stderr
    assert not calls


def test_shell_legacy_rpc_is_rejected_with_external_rollback_not_old_agent_upgrade(shell_runner):
    result, calls = shell_runner()
    assert result.returncode != 0
    assert "host_release_complete" not in result.stdout
    assert "Gateway handoff or functional verification failed" in result.stderr
    assert calls.count("$executor ") == 2 and "--rollback" in calls
    doops_calls = [line for line in calls.splitlines() if line.startswith("doops ")]
    assert any("capabilities" in line and "structuredSSH" in line for line in doops_calls)
    assert not any(word in ("upgrade", "ask", "rpc", "exec", "doops_agent_upgrade")
                   for line in doops_calls for word in line.split())
