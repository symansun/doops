"""One-time Unicom Compose upgrade, executed by host systemd, outside the Agent."""
import copy
import fcntl
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

import yaml


SOURCE = Path("/opt/doops-agent/compose.yaml")
STATE = Path("/var/lib/doops-releases/unicom1-042-20261108")
CONTAINER = "doops-agent"
OLD_ID = "fc58d9a6253b1f29b9185b2cbafcfa04e84a0d82b6d10018866963b6745c7335"
IMAGE = "docker.cnb.cool/l8ai/ai/doops.sh@sha256:fd20c510411d9dde8e2061ed390e3b91877781ade2d4400a39b715280524b02a"
COMMIT = "8683d67174e19a1750e9b19de99a0816cdb8ea79"
MOUNT = "/etc/kubernetes/admin.conf:/root/.kube/config:ro"


def run(*args, data=None, timeout=60):
    result = subprocess.run(args, input=data, capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{args[:3]} failed with exit {result.returncode}: {result.stderr[-1500:]}")
    return result.stdout


def inspect(name, image=False):
    args = ["docker", "image", "inspect"] if image else ["docker", "inspect"]
    return json.loads(run(*args, name))[0]


def compose(*args, data=None, timeout=60, file=SOURCE):
    return run("docker", "compose", "--project-directory", str(SOURCE.parent),
               "-p", "doops-agent", "-f", str(file), *args, data=data, timeout=timeout)


def config(original, image):
    desired = copy.deepcopy(original)
    assert set(desired["services"]) == {CONTAINER}
    service = desired["services"][CONTAINER]
    service["image"] = image
    service["volumes"].append(MOUNT)
    return yaml.safe_dump(desired, sort_keys=False)


def atomic_write(path, text):
    temporary = path.with_suffix(path.suffix + ".upgrade-tmp")
    with temporary.open("x") as stream:
        os.chmod(temporary, 0o600)
        stream.write(text)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def receipt(**fields):
    atomic_write(STATE / "receipt.json", json.dumps(fields, indent=2))
    print(json.dumps(fields), flush=True)


def verify_running(image_id):
    current = inspect(CONTAINER)
    assert current["Image"] == image_id
    assert current["State"]["Running"]
    assert current["State"].get("Health", {}).get("Status") == "healthy"
    assert current["HostConfig"]["NetworkMode"] == "host"
    assert current["HostConfig"]["PidMode"] == "host"
    assert current["HostConfig"]["Privileged"] is True
    return current["Id"]


def stop(signum, frame):
    raise RuntimeError(f"controller interrupted by signal {signum}")


def replace_and_accept(candidate_id, old_image, candidate_text, rollback_text):
    try:
        atomic_write(SOURCE, candidate_text)
        compose("up", "-d", "--no-deps", "--no-build", "--pull", "never",
                "--wait", "--wait-timeout", "180", CONTAINER, timeout=210)
        new_id = verify_running(candidate_id)
        assert new_id != OLD_ID
        receipt(phase="candidate-ready", containerID=new_id, image=IMAGE, commit=COMMIT)
        deadline = time.monotonic() + 600
        while time.monotonic() < deadline:
            acceptance = STATE / "acceptance.json"
            if acceptance.exists():
                evidence = json.loads(acceptance.read_text())
                assert evidence["containerID"] == new_id
                assert evidence["functionMarker"] == "RELEASE_SMOKE_UNICOM1_042"
                assert evidence["acceptanceSession"]
                break
            time.sleep(5)
        else:
            raise RuntimeError("Gateway/model acceptance missing after 600 seconds")
        assert verify_running(candidate_id) == new_id
        receipt(phase="complete", containerID=new_id, image=IMAGE, commit=COMMIT)
    except BaseException as error:
        print(json.dumps({"phase": "failed", "cause": str(error)}), flush=True)
        try:
            atomic_write(SOURCE, rollback_text)
            compose("up", "-d", "--no-deps", "--no-build", "--pull", "never",
                    "--wait", "--wait-timeout", "180", CONTAINER, timeout=210)
            restored_id = verify_running(old_image)
            receipt(phase="restored", containerID=restored_id,
                    imageID=old_image, cause=str(error))
        except BaseException as recovery_error:
            print(json.dumps({"phase": "recovery-failed", "cause": str(error),
                              "recovery": str(recovery_error)}), flush=True)
            raise RuntimeError(f"upgrade failed: {error}; recovery failed: {recovery_error}") from recovery_error
        raise


def main(apply=False):
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    with open("/run/lock/doops-unicom1-042.lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        old = inspect(CONTAINER)
        assert old["Id"] == OLD_ID, "baseline container changed"
        verify_running(old["Image"])
        labels = old["Config"]["Labels"]
        assert labels["com.docker.compose.project"] == "doops-agent"
        assert labels["com.docker.compose.project.config_files"] == str(SOURCE)
        assert labels["com.docker.compose.service"] == CONTAINER
        assert old["Config"]["Cmd"] == [
            "-gateway-url", "https://doops.l8ai.cn",
            "-cluster", "doops-unicom", "-instance", "unicom-1"
        ]
        live_kubeconfig = Path(f"/proc/{old['State']['Pid']}/root/root/.kube/config")
        assert os.readlink(live_kubeconfig) == "/host/root/.kube/config"
        assert Path("/root/.kube/config").read_bytes() == Path("/etc/kubernetes/admin.conf").read_bytes()
        assert not any(m["Destination"] == "/root/.kube/config" for m in old["Mounts"])
        candidate = inspect(IMAGE, image=True)
        assert candidate["Config"]["Labels"]["org.opencontainers.image.revision"] == COMMIT
        assert candidate["Config"]["Labels"]["org.l8ai.doagent.version"] == "0.2.42"
        inspect(old["Image"], image=True)  # The exact rollback image must still exist locally.
        original_text = SOURCE.read_text()
        original = yaml.safe_load(original_text)
        candidate_text = config(original, IMAGE)
        desired = json.loads(compose("config", "--format", "json", file="-", data=candidate_text))
        baseline = json.loads(compose("config", "--format", "json"))
        normalized = copy.deepcopy(desired)
        normalized["services"][CONTAINER]["image"] = baseline["services"][CONTAINER]["image"]
        added = [m for m in normalized["services"][CONTAINER]["volumes"]
                 if m["target"] == "/root/.kube/config"]
        assert len(added) == 1 and added[0]["source"] == "/etc/kubernetes/admin.conf"
        assert added[0]["read_only"] is True
        normalized["services"][CONTAINER]["volumes"].remove(added[0])
        assert normalized == baseline, "unexpected Compose configuration change"
        if not apply:
            print(json.dumps({"preflight": "ready", "oldContainerID": OLD_ID,
                              "candidateImageID": candidate["Id"],
                              "change": "pinned image and persistence of the identical existing kubeconfig"}))
            return

        assert not STATE.exists(), "single-use receipt already exists; inspect instead of replaying"
        assert run("docker", "run", "--rm", "--network", "none", "--entrypoint",
                   "/app/doops-agent", IMAGE, "-version").strip() == f"release=v1.3.123 commit={COMMIT}"
        STATE.mkdir(parents=True, mode=0o700)
        atomic_write(STATE / "original-compose.yaml", original_text)
        rollback_text = config(original, old["Image"])
        atomic_write(STATE / "rollback-compose.yaml", rollback_text)
        run("docker", "image", "tag", old["Image"], "doops-agent-rollback:unicom1-042-20261108")
        receipt(phase="prepared", oldContainerID=OLD_ID, oldImageID=old["Image"], image=IMAGE)
        replace_and_accept(candidate["Id"], old["Image"], candidate_text, rollback_text)


if __name__ == "__main__":
    if sys.argv[1:] not in (["--dry-run"], ["--apply"]):
        raise SystemExit("usage: compose-upgrade.py --dry-run|--apply")
    main(apply=sys.argv[1] == "--apply")
