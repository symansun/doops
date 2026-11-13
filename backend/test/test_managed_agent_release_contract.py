import json
import os
import shutil
import signal
import subprocess
import sys
from pathlib import Path

import pytest
import yaml


ROOT = Path(__file__).resolve().parents[1]
RELEASE = ROOT / "scripts" / "release-managed-agent.sh"
CHART = ROOT / "deploy" / "helm" / "doops-agent" / "templates" / "deployment.yaml"
VALUES = ROOT / "deploy" / "helm" / "doops-agent" / "values.yaml"
SCHEMA = ROOT / "deploy" / "helm" / "doops-agent" / "values.schema.json"
PINNED_IMAGE = "registry.example.test/doops-agent@sha256:" + "a" * 64
RELEASE_ARGS = [
    "--target", "synthetic-agent",
    "--session", "audit-upgrade-test",
    "--release", "doops-agent",
    "--namespace", "agents-test",
    "--image", PINNED_IMAGE,
]


@pytest.mark.parametrize("host_access", [None, True, False])
def test_agent_chart_hands_off_without_dropping_the_live_replica(host_access):
    command = [
        "helm", "template", "doops-agent", str(VALUES.parent),
        "--set", "workload.name=doops-agent",
        "--set", "gateway.url=https://gateway.example.test",
        "--set", "gateway.cluster=test-cluster",
        "--set", "gateway.instance=test-agent",
        "--set", "gateway.enrollmentTokenSecret=doops-agent-enrollment",
        "--set", "image.repository=registry.example.test/doops-agent",
        "--set", "image.tag=test",
    ]
    if host_access is not None:
        command.extend(["--set", f"hostAccess.enabled={str(host_access).lower()}"])
    result = subprocess.run(command, capture_output=True, text=True, check=True)
    deployment = next(item for item in yaml.safe_load_all(result.stdout) if item)
    spec = deployment["spec"]
    pod = spec["template"]["spec"]
    container = pod["containers"][0]
    assert spec["strategy"] == {
        "type": "RollingUpdate",
        "rollingUpdate": {"maxUnavailable": 0, "maxSurge": 1},
    }
    assert pod["terminationGracePeriodSeconds"] == 30
    assert spec["minReadySeconds"] == 5
    assert pod["hostNetwork"] is False
    assert pod["hostPID"] is (host_access is not False)
    assert "/app/sandbox-entrypoint.sh" in container["command"][-1]
    enrollment = next(item for item in container["env"] if item["name"] == "DOOPS_AGENT_ENROLL_TOKEN")
    assert enrollment["valueFrom"]["secretKeyRef"] == {
        "name": "doops-agent-enrollment", "key": "token",
    }
    assert "value" not in enrollment
    for name, endpoint in (
        ("startupProbe", "live"), ("readinessProbe", "ready"), ("livenessProbe", "live")
    ):
        probe = container[name]["exec"]["command"][-1]
        assert f"http://127.0.0.1:42222/health/{endpoint}" in probe
        assert ("pgrep" in probe) is (host_access is False)


def test_agent_chart_keeps_open_registration_without_enrollment_secret():
    result = subprocess.run(
        [
            "helm", "template", "doops-agent", str(VALUES.parent),
            "--set", "workload.name=doops-agent",
            "--set", "gateway.url=https://gateway.example.test",
            "--set", "gateway.cluster=test-cluster",
            "--set", "gateway.instance=test-agent",
            "--set", "image.repository=registry.example.test/doops-agent",
            "--set", "image.tag=test",
        ],
        capture_output=True, text=True, check=True,
    )
    deployment = next(item for item in yaml.safe_load_all(result.stdout) if item)
    names = [item["name"] for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]]
    assert "DOOPS_AGENT_ENROLL_TOKEN" not in names


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


@pytest.fixture
def run_release(tmp_path):
    """Run the real shell entrypoint with no access to operator tools or login."""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    for name in ("bash", "cat"):
        (bin_dir / name).symlink_to(shutil.which(name))
    calls_file = tmp_path / "calls.jsonl"
    kubeconfig = tmp_path / "kubeconfig"
    kubeconfig.write_text("SYNTHETIC_KUBECONFIG_PAYLOAD_MUST_NOT_BE_READ")
    tool = f"#!{sys.executable}\n" + """
import json
import os
import signal
import sys
from pathlib import Path

name = Path(sys.argv[0]).name
with open(os.environ["RELEASE_TEST_CALLS"], "a") as stream:
    stream.write(json.dumps({
        "tool": name, "args": sys.argv[1:],
        "kubeconfig": os.environ.get("KUBECONFIG"),
    }) + "\\n")
if name != "doops":
    raise SystemExit("local controller tool must never execute")
if os.environ.get("RELEASE_TEST_SIGNAL"):
    os.kill(os.getpid(), signal.SIGTERM)
sys.stdout.write(os.environ["RELEASE_TEST_STDOUT"])
sys.stderr.write(os.environ["RELEASE_TEST_STDERR"])
raise SystemExit(int(os.environ["RELEASE_TEST_STATUS"]))
"""
    for name in ("doops", "helm", "kubectl", "docker", "ssh", "scp", "curl", "wget"):
        executable = bin_dir / name
        executable.write_text(tool)
        executable.chmod(0o755)

    def run(args=None, *, status=0, stdout="", stderr="", terminate=False, cli=True):
        if not cli:
            (bin_dir / "doops").unlink()
        env = {
            **os.environ,
            "PATH": str(bin_dir),
            "KUBECONFIG": str(kubeconfig),
            "RELEASE_TEST_CALLS": str(calls_file),
            "RELEASE_TEST_STATUS": str(status),
            "RELEASE_TEST_STDOUT": stdout,
            "RELEASE_TEST_STDERR": stderr,
            "RELEASE_TEST_SIGNAL": "1" if terminate else "",
        }
        result = subprocess.run(
            ["bash", str(RELEASE), *(RELEASE_ARGS if args is None else args)],
            cwd=ROOT, env=env, text=True, capture_output=True, check=False, timeout=10,
        )
        calls = [json.loads(line) for line in calls_file.read_text().splitlines()] if calls_file.exists() else []
        return result, calls

    return run


@pytest.mark.parametrize("dry_run", [False, True])
def test_release_delegates_the_whole_transaction_to_gateway_upgrade(run_release, dry_run):
    output = "PREVIEW parameters only\n" if dry_run else "OK upgrade Job and protocol verified\n"
    args = RELEASE_ARGS + (["--dry-run"] if dry_run else [])
    result, calls = run_release(args, stdout=output)

    assert result.returncode == 0
    assert result.stdout == output
    assert result.stderr == ""
    assert calls == [{
        "tool": "doops",
        "args": [
            "-session", "audit-upgrade-test", "upgrade",
            "--target", "synthetic-agent", "--mode", "k8s",
            "--release", "doops-agent", "--namespace", "agents-test",
            "--image", PINNED_IMAGE,
            *(["--dry-run"] if dry_run else []),
        ],
        "kubeconfig": None,
    }]


@pytest.mark.parametrize("status,message", [
    (1, "forbidden: target grant required"),
    (1, "configured target is offline"),
    (1, "Gateway connection refused"),
    (1, "audit_unavailable"),
    (1, "audit_completion_failed; execution outcome requires verification"),
    (2, "unknown tool: doops_agent_upgrade"),
    (1, "preflight failed, nothing was changed: rollback_baseline_image_unavailable"),
    (1, "release commit mismatch; automatic rollback completed"),
    (1, "automatic rollback failed; intervention required"),
    (127, "installed CLI does not support upgrade"),
])
def test_gateway_failure_never_retries_or_runs_a_local_controller(run_release, status, message):
    result, calls = run_release(status=status, stdout="upgrade progress\n", stderr=message + "\n")
    assert result.returncode == status
    assert result.stdout == "upgrade progress\n"
    assert result.stderr == message + "\n"
    assert len(calls) == 1
    assert calls[0]["tool"] == "doops"
    assert calls[0]["args"][2] == "upgrade"
    assert calls[0]["kubeconfig"] is None


def test_release_preserves_cli_process_signal_semantics(run_release):
    result, calls = run_release(terminate=True)
    assert result.returncode == -signal.SIGTERM
    assert len(calls) == 1
    assert calls[0]["tool"] == "doops"
    assert result.stdout == result.stderr == ""


@pytest.mark.parametrize("flag", [
    "--kubeconfig", "--values", "--chart-dir", "--cluster", "--instance",
    "--timeout", "--handoff-timeout", "--proxy-allowed-hosts", "--host-alias", "--version",
])
@pytest.mark.parametrize("equals", [False, True])
def test_retired_options_fail_before_any_tool_and_do_not_echo_payload(run_release, flag, equals):
    canary = "SYNTHETIC_RETIRED_ARGUMENT_MUST_NOT_BE_LOGGED"
    option = [f"{flag}={canary}"] if equals else [flag, canary]
    result, calls = run_release(RELEASE_ARGS + option)
    assert result.returncode == 2
    assert f"{flag} is retired" in result.stderr
    assert canary not in result.stdout + result.stderr
    assert calls == []


@pytest.mark.parametrize("dry_run", [False, True])
def test_new_cluster_install_is_closed_even_with_local_kubeconfig(run_release, dry_run):
    args = [
        "--release", "doops-agent", "--namespace", "agents-test",
        "--image", PINNED_IMAGE, "--values", "/synthetic/complete-values.yaml",
        "--kubeconfig", "/synthetic/kubeconfig",
        *(["--dry-run"] if dry_run else []),
    ]
    result, calls = run_release(args)
    assert result.returncode == 2
    assert "--values is retired" in result.stderr
    assert calls == []


@pytest.mark.parametrize("flag", ["--target", "--session", "--release", "--namespace", "--image"])
@pytest.mark.parametrize("value", [None, "", "   ", "--dry-run"])
def test_required_inputs_cannot_be_omitted_or_misparsed(run_release, flag, value):
    index = RELEASE_ARGS.index(flag)
    args = RELEASE_ARGS[:index] + RELEASE_ARGS[index + 2:]
    if value is not None:
        args.extend([flag, value])
    result, calls = run_release(args)
    assert result.returncode == 2
    assert flag in result.stderr
    assert calls == []


@pytest.mark.parametrize("image", [
    "registry.example.test/doops-agent:v1.2.3",
    "registry.example.test/doops-agent",
    "registry.example.test/doops-agent@sha256:" + "a" * 63,
    "registry.example.test/doops-agent@sha256:" + "z" * 64,
    "registry.example.test/doops-agent@sha256:" + "a" * 64 + "\n",
])
def test_image_must_be_pinned_without_local_resolution(run_release, image):
    result, calls = run_release(RELEASE_ARGS[:-1] + [image])
    assert result.returncode == 2
    assert "--image must be repository@sha256:<64-hex>" in result.stderr
    assert calls == []


@pytest.mark.parametrize("extra", [
    ["unexpected-positional"],
    ["--token", "SYNTHETIC_TOKEN_MUST_NOT_BE_LOGGED"],
    ["--allow-missing-rollback-baseline"],
    ["--rollback"],
])
def test_unknown_arguments_cannot_change_or_bypass_the_transaction(run_release, extra):
    result, calls = run_release(RELEASE_ARGS + extra)
    assert result.returncode == 2
    assert "unsupported argument" in result.stderr
    assert "SYNTHETIC_TOKEN_MUST_NOT_BE_LOGGED" not in result.stdout + result.stderr
    assert calls == []


def test_missing_cli_cannot_reach_local_tools(run_release):
    result, calls = run_release(cli=False)
    assert result.returncode == 1
    assert "install the published doops CLI" in result.stderr
    assert calls == []


def test_help_states_preview_and_first_upgrade_limits_without_running_tools(run_release):
    result, calls = run_release(["--help"])
    assert result.returncode == 0
    assert "Gateway parameter preview only" in result.stdout
    assert "does not implement that first-upgrade path" in result.stdout
    assert "doops -session <s> exec --target <t>" in result.stdout
    assert "backup" in result.stdout and "automatic rollback" in result.stdout
    assert calls == []
