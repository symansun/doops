#!/usr/bin/env python3
"""External Docker Agent release transaction; never run inside the Agent."""

import argparse
import copy
import fcntl
import http.client
import json
import os
import re
import signal
import socket
import subprocess
import sys
import time
from pathlib import Path
from urllib.parse import quote

STATE_ROOT = Path("/var/lib/doops-host-releases")
LOCK_ROOT = Path("/run/lock")


class DockerAPIError(RuntimeError):
    def __init__(self, method, path, status):
        self.status = status
        super().__init__("docker_api_failed: %s %s HTTP %s" % (method, path.split("?")[0], status))


class DockerConnection(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect("/var/run/docker.sock")


def docker_api(method, path, body=None):
    connection = DockerConnection("localhost", timeout=120)
    try:
        payload = None if body is None else json.dumps(body).encode()
        connection.request(method, path, payload, {"Content-Type": "application/json"})
        response = connection.getresponse()
        data = response.read()
        if response.status >= 400:
            # Docker errors can echo configuration. Do not publish their bodies.
            raise DockerAPIError(method, path, response.status)
        return json.loads(data) if data else None
    finally:
        connection.close()


def cli(*args):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
    if result.returncode:
        raise RuntimeError("release_command_failed: " + args[0])
    return result.stdout.decode().strip()


def identity(config):
    values = {}
    for entry in config.get("Env") or []:
        key, _, value = entry.partition("=")
        values[key] = value
    command = config.get("Cmd") or []
    result = {}
    for field in ("cluster", "instance"):
        value = values.get("DOOPS_GATEWAY_" + field.upper(), "")
        for index, argument in enumerate(command):
            if argument in ("-" + field, "--" + field) and index + 1 < len(command):
                value = command[index + 1]
            elif argument.startswith("-" + field + "="):
                value = argument.split("=", 1)[1]
        result[field] = value
    return result


def agent_port(config):
    command = config.get("Cmd") or []
    port = "42222"
    for index, argument in enumerate(command):
        if argument == "-port" and index + 1 < len(command):
            port = command[index + 1]
        elif argument.startswith("-port="):
            port = argument.split("=", 1)[1]
    if not port.isdigit() or not 1 <= int(port) <= 65535:
        raise RuntimeError("invalid_agent_health_port")
    return port


def replacement_config(original, image, image_info):
    config = copy.deepcopy(original["Config"])
    config["Image"] = image
    config["Labels"] = dict(config.get("Labels") or {})
    config["Labels"].update(image_info.get("Config", {}).get("Labels") or {})
    config["HostConfig"] = copy.deepcopy(original["HostConfig"])
    config["HostConfig"]["AutoRemove"] = False
    return config


def inspect_container(name):
    return docker_api("GET", "/containers/%s/json" % quote(name, safe=""))


def save_state(directory, state):
    temporary = directory / "state.tmp"
    temporary.write_text(json.dumps(state, sort_keys=True) + "\n")
    os.chmod(temporary, 0o600)
    temporary.replace(directory / "state.json")


def restore(state, container, directory):
    if state.get("new_id") == state["old_id"]:
        raise RuntimeError("replacement_identity_matches_old")
    old = inspect_container(state["old_id"])
    if old["Id"] != state["old_id"] or old["Name"] not in ("/" + container, "/" + state["backup"]):
        raise RuntimeError("old_container_ownership_changed")
    if state.get("new_id"):
        try:
            current = inspect_container(state["new_id"])
        except DockerAPIError as error:
            if error.status != 404 or state.get("new_removal_started") is not True:
                raise
            current = None
        if current is not None:
            if current["Id"] != state["new_id"] or current["Name"] not in ("/" + container, "/" + state["replacement"]):
                raise RuntimeError("replacement_ownership_changed")
            # Persist intent before DELETE, including an interruption before its reply.
            state["new_removal_started"] = True
            save_state(directory, state)
            docker_api("DELETE", "/containers/%s?force=1" % state["new_id"])
    if old["Name"] != "/" + container:
        docker_api("POST", "/containers/%s/rename?name=%s" % (old["Id"], quote(container, safe="")))
    if not old["State"]["Running"]:
        docker_api("POST", "/containers/%s/start" % old["Id"])
    if not inspect_container(old["Id"])["State"]["Running"]:
        raise RuntimeError("old_container_did_not_restart")


def release(args):
    if args.dry_run and args.rollback:
        raise RuntimeError("dry_run_cannot_mutate_rollback")
    if os.geteuid() != 0:
        raise RuntimeError("host_root_required")
    if Path("/.dockerenv").exists() or os.environ.get("KUBERNETES_SERVICE_HOST"):
        raise RuntimeError("external_host_controller_required")
    for value in (args.container, args.session, args.cluster, args.instance):
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,63}", value):
            raise RuntimeError("invalid_release_identity")
    if not re.fullmatch(r"[^\s@]+@sha256:[a-f0-9]{64}", args.image):
        raise RuntimeError("immutable_image_required")
    if not re.fullmatch(r"[a-f0-9]{40}", args.source_commit):
        raise RuntimeError("source_commit_required")
    lock = open(LOCK_ROOT / ("doops-host-release-%s.lock" % args.container), "a")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    directory = STATE_ROOT / args.container / args.session
    if args.rollback:
        try:
            state = json.loads((directory / "state.json").read_text())
        except FileNotFoundError:
            raise RuntimeError("rollback_receipt_missing; external intervention required") from None
        if state.get("phase") == "preflight":
            print("host_rollback_not_started container=%s phase=preflight" % args.container)
            return
        restore(state, args.container, directory)
        state["phase"] = "rolled_back"
        save_state(directory, state)
        print("host_rollback_restored old_id=%s" % state["old_id"])
        return
    if not args.dry_run:
        directory.mkdir(parents=True, mode=0o700, exist_ok=False)
        os.chmod(directory, 0o700)
        save_state(directory, {"phase": "preflight", "image": args.image, "source_commit": args.source_commit})
    original = inspect_container(args.container)
    if not original["State"]["Running"] or original["HostConfig"].get("AutoRemove"):
        raise RuntimeError("running_recoverable_container_required")
    if original["Config"].get("Labels", {}).get("com.docker.compose.project"):
        raise RuntimeError("compose_managed_container_requires_its_compose_controller")
    if identity(original["Config"]) != {"cluster": args.cluster, "instance": args.instance}:
        raise RuntimeError("agent_identity_mismatch")
    for mount in original.get("Mounts") or []:
        destination = mount["Destination"].rstrip("/")
        if destination == "" or destination == "/app" or destination.startswith("/app/"):
            raise RuntimeError("mount_masks_release_artifact")
    settings_sources = []
    for mount in original.get("Mounts") or []:
        destination = mount["Destination"].rstrip("/")
        if destination == "/opt/doagent_config/settings.json":
            settings_sources.append(mount["Source"])
        elif destination == "/opt/doagent_config":
            settings_sources.append(str(Path(mount["Source"]) / "settings.json"))
    if len(settings_sources) != 1 or not Path(settings_sources[0]).is_file():
        raise RuntimeError("authoritative_doagent_settings_mount_required")
    if not args.dry_run:
        cli("docker", "pull", "--platform", "linux/amd64", args.image)
    image_info = docker_api("GET", "/images/%s/json" % quote(args.image, safe=""))
    labels = image_info.get("Config", {}).get("Labels") or {}
    if labels.get("org.opencontainers.image.revision") != args.source_commit or labels.get("org.opencontainers.image.version") != args.release:
        raise RuntimeError("image_provenance_mismatch")
    expected_version = "release=%s commit=%s" % (args.release, args.source_commit)
    if not args.dry_run and cli("docker", "run", "--rm", "--network", "none", "--entrypoint", "/app/doops-agent", args.image, "-version") != expected_version:
        raise RuntimeError("image_binary_provenance_mismatch")
    config = replacement_config(original, args.image, image_info)
    health_port = agent_port(config)
    if args.dry_run:
        print("preflight_ready container=%s identity=%s/%s source=%s" % (args.container, args.cluster, args.instance, args.source_commit))
        return
    backup = args.container + "-rollback-" + args.session
    replacement = args.container + "-replacement-" + args.session
    state = {"old_id": original["Id"], "backup": backup, "replacement": replacement, "image": args.image, "source_commit": args.source_commit, "phase": "prepared"}
    save_state(directory, state)
    new_id = None
    try:
        new_id = docker_api("POST", "/containers/create?name=%s" % quote(replacement, safe=""), config)["Id"]
        state.update(new_id=new_id)
        save_state(directory, state)
        state["phase"] = "replacing"
        save_state(directory, state)
        docker_api("POST", "/containers/%s/stop?t=30" % original["Id"])
        docker_api("POST", "/containers/%s/rename?name=%s" % (original["Id"], quote(backup, safe="")))
        docker_api("POST", "/containers/%s/rename?name=%s" % (new_id, quote(args.container, safe="")))
        docker_api("POST", "/containers/%s/start" % new_id)
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            current = inspect_container(new_id)
            if not current["State"]["Running"]:
                raise RuntimeError("replacement_exited")
            try:
                output = cli("docker", "exec", new_id, "/app/doops-agent", "-version")
                if output != expected_version or current["Image"] != image_info["Id"]:
                    raise RuntimeError("running_artifact_mismatch")
                cli("docker", "exec", new_id, "curl", "-fsS", "--max-time", "2", "http://127.0.0.1:%s/health/ready" % health_port)
                state["phase"] = "host_ready"
                save_state(directory, state)
                print("host_upgrade_ready container=%s id=%s backup=%s receipt=%s" % (args.container, new_id, backup, directory / "state.json"))
                return
            except RuntimeError as error:
                if str(error) != "release_command_failed: docker":
                    raise
                time.sleep(2)
        raise RuntimeError("gateway_handoff_or_functional_probe_failed")
    except BaseException:
        try:
            restore(state, args.container, directory)
            state["phase"] = "rolled_back"
            save_state(directory, state)
            print("host_rollback_restored old_id=%s" % original["Id"], file=sys.stderr)
        except BaseException:
            raise RuntimeError("host_rollback_failed; external intervention required") from None
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("container", "session", "cluster", "instance", "image", "source-commit", "release"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--rollback", action="store_true")
    args = parser.parse_args()
    def interrupted(_number, _frame):
        raise RuntimeError("release_interrupted")
    for name in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(name, interrupted)
    try:
        release(args)
    except BaseException as error:
        print("host_release_failed: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
