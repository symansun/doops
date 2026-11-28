"""Exercise the external release wrapper's failure and live-probe boundaries."""

import os
import shutil
import subprocess
from pathlib import Path

import pytest

from test_host_agent_release import IMAGE, SOURCE, VERSION


SHELL = Path(__file__).resolve().parents[1] / "scripts" / "release-host-agent.sh"


@pytest.fixture
def run_release(tmp_path):
    calls = tmp_path / "calls.log"
    boundary = tmp_path / "boundaries.sh"
    boundary.write_text(r'''
doops() {
  printf 'doops %s\n' "$*" >> "$CALLS"
  case "$*" in
    list) printf '%s\n' 'host-target gateway test-cluster/test-node' ;;
    *' targets '*) printf 'test-cluster test-node false idle 0 0 %s\n' "$SOURCE" ;;
    *' capabilities '*) return 0 ;;
    *' ssh '*)
      [[ "$MODE" != ssh ]] || return 1
      printf '%s\n' 'ssh: connect to host 127.0.0.1 port 1: Connection refused' 'Error: remote error: execution_outcome_unknown'
      return 1 ;;
    *' exec --bg '*)
      [[ "$MODE" != background ]] || return 1
      printf '%s\n' 'host-release-host-release-test-background' 'ExitCode: 0' ;;
    *' exec '*'-version'*) printf 'release=%s commit=%s\n' "$VERSION" "$SOURCE" ;;
    *' exec '*)
      [[ "$MODE" != output ]] || return 0
      printf '%s\n' 'host-release-host-release-test-stdout' 'host-release-host-release-test-stderr' ;;
    *) return 99 ;;
  esac
}
ssh() {
  printf 'ssh %s\n' "$*" >> "$CALLS"
  case "${!#}" in
    'mktemp '*) printf '%s\n' '/tmp/doops-host-release.synthetic' ;;
    *'--rollback'*)
      [[ "$MODE" != rollback ]] || return 1
      printf '%s\n' 'host_rollback_restored' ;;
    *'$executor '*)
      [[ "$MODE" != transport && "$MODE" != rollback ]] || return 255
      printf '%s\n' 'host_upgrade_ready' ;;
    'test '*|'rm -f '*) return 0 ;;
    *) return 99 ;;
  esac
}
scp() { return 0; }
sha256sum() { printf '%s %s\n' "$HASH" "$1"; }
sleep() { SECONDS=$((SECONDS + 121)); }
''')
    command = [shutil.which("bash"), str(SHELL),
               "--target", "host-target", "--session", "host-release-test",
               "--host", "host.example.invalid", "--user", "operator", "--port", "2022",
               "--container", "doops-agent", "--cluster", "test-cluster",
               "--instance", "test-node", "--image", IMAGE,
               "--source-commit", SOURCE, "--release", VERSION]

    def run(mode):
        env = {"PATH": os.environ.get("PATH", ""), "HOME": str(tmp_path),
               "LC_ALL": "C", "BASH_ENV": str(boundary), "CALLS": str(calls),
               "SOURCE": SOURCE, "VERSION": VERSION, "HASH": "f" * 64, "MODE": mode}
        result = subprocess.run(command, env=env, capture_output=True, text=True,
                                check=False, timeout=10)
        return result, calls.read_text()

    return run


def test_transport_failure_still_requests_external_rollback(run_release):
    result, calls = run_release("transport")
    assert result.returncode == 255
    assert "--rollback" in calls
    assert "host_release_complete" not in result.stdout


def test_rollback_failure_is_reported_and_never_success(run_release):
    result, calls = run_release("rollback")
    assert result.returncode == 1
    assert "--rollback" in calls
    assert "Host rollback failed; external intervention required" in result.stderr
    assert "host_release_complete" not in result.stdout


@pytest.mark.parametrize("mode", ["output", "background", "ssh"])
def test_version_and_capabilities_are_not_functional_acceptance(run_release, mode):
    result, calls = run_release(mode)
    assert result.returncode != 0
    assert "Gateway handoff or functional verification failed" in result.stderr
    assert "--rollback" in calls
    assert "host_release_complete" not in result.stdout


def test_release_requires_all_real_execution_probes(run_release):
    result, calls = run_release("success")
    assert result.returncode == 0, result.stderr
    assert "host_release_complete" in result.stdout
    assert "--rollback" not in calls
    assert "exec --bg" in calls and " ssh " in calls
    assert calls.count(" exec ") == 4
