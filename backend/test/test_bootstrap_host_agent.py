import json
import os
from pathlib import Path
import subprocess

import pytest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/bootstrap-host-agent.sh"


def run_bootstrap(tmp_path, failure="", extra_args=()):
    scripts = tmp_path / "scripts"
    scripts.mkdir()
    config_dir = tmp_path / "installed"
    os_release = tmp_path / "os-release"
    os_release.write_text('ID=ubuntu\nVERSION="24.04.3 LTS (Noble Numbat)"\nVERSION_CODENAME=noble\n')
    script = scripts / SCRIPT.name
    script.write_text(
        SCRIPT.read_text()
        .replace("/opt/doops-agent", str(config_dir))
        .replace("/run/lock/doops-agent-bootstrap.lock", str(tmp_path / "lock"))
        .replace(". /etc/os-release", f". '{os_release}'")
    )
    commands = tmp_path / "commands"
    commands.mkdir()
    for name, body in {
        "id": "echo 0",
        "flock": "exit 0",
        "apt-get": "exit 0",
        "systemctl": "exit 0",
        "uname": "echo x86_64",
        "docker": """
printf '%s\\n' "$*" >> "$TEST_CALLS"
case "$*" in
  "ps -aq"*) [[ "$TEST_FAILURE" != existing ]] || echo existing-container ;;
  "image inspect"*) printf 'docker.cnb.cool/l8ai/ai/doops.sh@sha256:%064d\\n' 0 ;;
  run*) printf 'release=v1.3.48 commit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\n' ;;
  exec*) [[ "$TEST_FAILURE" != version ]] || exit 1
    printf 'release=v1.3.48 commit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\n' ;;
  "compose "*" up "*) [[ "$TEST_FAILURE" != unhealthy ]] ;;
  inspect*) if [[ "$TEST_FAILURE" == health ]]; then echo unhealthy; else echo healthy; fi ;;
esac
""",
    }.items():
        command = commands / name
        command.write_text("#!/bin/bash\nset -e\n" + body + "\n")
        command.chmod(0o755)
    log = tmp_path / "calls"
    result = subprocess.run(
        ["bash", str(script), "--version", "v1.3.48",
         "--gateway", "https://gateway.example.com",
         "--cluster", "doops-test", "--instance", "node-1", *extra_args],
        env={**os.environ, "PATH": f"{commands}:{os.environ['PATH']}",
             "TEST_FAILURE": failure, "TEST_CALLS": str(log)},
        capture_output=True, text=True, timeout=20,
    )
    return result, config_dir, log.read_text() if log.exists() else ""


def test_bootstrap_writes_pinned_persistent_deployment_and_waits(tmp_path):
    result, path, calls = run_bootstrap(tmp_path)
    assert result.returncode == 0, result.stderr
    compose = json.loads((path / "compose.yaml").read_text())
    service = compose["services"]["agent"]
    assert "@sha256:" in service["image"]
    assert service["command"][-4:] == ["-cluster", "doops-test", "-instance", "node-1"]
    assert not service.get("ports")
    assert not any("docker.sock" in value for value in service["volumes"])
    # The model provider comes from the Gateway; nothing model-related is installed.
    assert not (path / "settings.json").exists()
    assert not any("doagent_config" in value for value in service["volumes"])
    assert "agent-state:/root/.agent" in service["volumes"]
    assert "up -d --wait --wait-timeout 240" in calls
    assert "pull --platform linux/amd64 docker.cnb.cool/l8ai/ai/doops.sh:v1.3.48" in calls
    assert "Agent registered: doops-test/node-1" in result.stdout


@pytest.mark.parametrize("failure", ["unhealthy", "health", "version"])
def test_bootstrap_stops_failed_container_without_claiming_success(tmp_path, failure):
    result, path, calls = run_bootstrap(tmp_path, failure)
    assert result.returncode != 0
    assert " stop\n" in calls
    assert path.exists()
    assert "Agent registered:" not in result.stdout


def test_bootstrap_refuses_existing_agent(tmp_path):
    result, path, calls = run_bootstrap(tmp_path, "existing")
    assert result.returncode != 0
    assert not path.exists()
    assert "pull " not in calls


def test_bootstrap_rejects_removed_settings_flag_before_pull(tmp_path):
    result, path, calls = run_bootstrap(tmp_path, extra_args=("--settings", "/secure/settings.json"))
    assert result.returncode == 2
    assert not path.exists()
    assert "pull " not in calls


def test_docker_repository_setup_can_resume_but_does_not_overwrite_conflicts(tmp_path):
    text = SCRIPT.read_text()
    source = text.split("<<'PY'\n", 1)[1].split("\nPY", 1)[0]
    path = tmp_path / "docker.sources"
    source = source.replace("/etc/apt/sources.list.d/docker.sources", str(path))
    for _ in range(2):
        result = subprocess.run(
            ["python3", "-", "ubuntu", "noble", "amd64"],
            input=source, text=True, capture_output=True, timeout=5,
        )
        assert result.returncode == 0, result.stderr
    path.write_text("different repository\n")
    result = subprocess.run(
        ["python3", "-", "ubuntu", "noble", "amd64"],
        input=source, text=True, capture_output=True, timeout=5,
    )
    assert result.returncode != 0
    assert path.read_text() == "different repository\n"


@pytest.mark.parametrize("args", [[], ["--version"], ["--version", "latest"]])
def test_bootstrap_rejects_incomplete_input_without_mutation(args):
    result = subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, timeout=5)
    assert result.returncode == 2
