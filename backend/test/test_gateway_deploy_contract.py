import os
import subprocess
from pathlib import Path

import pytest


ROOT = Path(__file__).resolve().parents[1]
DEPLOY = ROOT / "scripts" / "deploy-gateway.sh"
FETCH = ROOT / "scripts" / "fetch-gateway-release.sh"


def test_gateway_deploy_detects_systemd_unit_without_pipefail_pipeline():
    text = DEPLOY.read_text()

    assert "systemctl list-unit-files" not in text
    assert "sudo -n systemctl cat doops-gateway.service >/dev/null" in text
    assert "REMOTE_LOCK=\"/run/lock/doops-gateway-deploy.lock\"" in text
    assert "REMOTE_TMP=\"/tmp/doops-gateway-deploy-${DEPLOY_ID}\"" in text
    assert "ControlMaster=auto" in text
    assert "ControlPersist=30" in text
    assert "ControlPath=${SSH_CONTROL_PATH}" in text
    assert "wait_for_local_gateway()" in text
    assert 'wait_for_local_gateway "${READINESS_TIMEOUT}"' in text
    assert "--readiness-timeout" in text


def test_gateway_deploy_bootstraps_external_key_only_without_existing_ciphertext():
    text = DEPLOY.read_text()

    assert "password_ciphertext" in text
    assert "payload_ciphertext" in text
    assert "secrets.token_hex(32)" in text
    assert "gateway encrypted data exists but no legacy secret key is available" in text


def test_gateway_deploy_can_verify_with_configured_doops_target_without_exporting_token():
    text = DEPLOY.read_text()

    assert "--verify-target" in text
    assert "--verify-session" in text
    assert "--verify-token-file" not in text
    assert "VERIFY_TOKEN" not in text


def test_gateway_release_fetches_the_ci_binary_from_release_assets():
    text = FETCH.read_text()

    assert "--release-tag" in text
    assert "--source-commit" in text
    assert "^[0-9a-f]{40}$" in text
    assert "doops-gateway-linux-amd64" in text
    assert "checksums.txt" in text
    assert "checksum mismatch" in text
    assert 'for marker in "${RELEASE_TAG}" "${SOURCE_COMMIT}"' in text

    build = (ROOT / "scripts" / "build-cli.sh").read_text()
    assert "doops-gateway-linux-amd64" in build

    dockerfile = (ROOT / "Dockerfile").read_text()
    assert "COPY --from=builder /app/doops-gateway" not in dockerfile
    assert "! test -e /app/doops-gateway" in dockerfile


def test_gateway_deploy_supports_mysql_backend_and_prunes_old_rollbacks():
    text = DEPLOY.read_text()

    assert "DOOPS_GATEWAY_STORE=" in text
    assert "DOOPS_GATEWAY_DATA_DIR=" in text
    assert "tail -n +6" in text


def test_gateway_deploy_rejects_binary_without_exact_source_commit():

    text = DEPLOY.read_text()

    assert "--source-commit SHA" in text
    assert "a real Gateway deployment requires --source-commit" in text
    assert 'strings -a "${LOCAL_BIN}"' in text
    assert 'grep -Fxx "${SOURCE_COMMIT}" >/dev/null' in text
    assert "Gateway binary does not contain source commit" in text


@pytest.mark.parametrize(("mode", "routes", "expected"), [
    ("normal", ["first"], "success"),
    ("normal", ["first", "second"], "success"),
    ("enrolled", ["first", "second"], "success"),
    ("unenrolled", ["first", "second"], "success"),
    ("foreign-warning", ["first", "second"], "baseline_failure"),
    ("alias", ["first", "alias"], "baseline_failure"),
    ("denied", ["first", "second"], "baseline_failure"),
    ("empty", ["first", "second"], "baseline_failure"),
    ("ambiguous", ["first", "second"], "baseline_failure"),
    ("missing", ["first", "second"], "rollback"),
    ("swapped", ["first", "second"], "rollback"),
])
def test_route_reconnect_gate(tmp_path, mode, routes, expected):
    # Exercise the script entrypoint; SSH, uploads, health, CLI and waiting stay local stubs.
    def stub(name, body):
        path = tmp_path / name
        path.write_text("#!/bin/bash\nset -eu\n" + body + "\n")
        path.chmod(0o700)

    stub("ssh", r'''case "${!#}" in
      "sudo -n sha256sum "*) echo "dummy-digest  old-binary" ;;
      *"install -m 0755"*".rollback-"*) echo rollback >> "$TRACE"; touch "$FIX/restored" ;;
    esac''')
    stub("scp", 'echo upload >> "$TRACE"; touch "$FIX/uploaded"')
    stub("curl", "exit 0")
    stub("sha256sum", 'echo "0123456789abcdef  fixture"')
    stub("strings", 'echo "$COMMIT"')
    stub("sleep", "exit 0")
    stub("seq", 'printf "1\\n2\\n"')  # Bound the simulation, not the production deadline.
    stub("doops", r'''
    [[ "$#" == 5 && "$1" == -session && "$2" == test && "$3" == targets && "$4" == --target ]]
    route=$5
    stage=before
    if [[ -e "$FIX/uploaded" ]]; then stage=after; fi
    if [[ -e "$FIX/restored" ]]; then stage=restored; fi
    echo "$stage:$route" >> "$TRACE"
    identity=$route
    if [[ $MODE == alias && $route == alias ]]; then identity=first; fi
    case "$MODE" in
      enrolled|unenrolled|foreign-warning) echo 'CLUSTER INSTANCE BUSY STATUS ACTIVE QUEUED ENROLLED RELEASE_COMMIT RESOURCES LAST_SEEN' ;;
      *) echo 'CLUSTER INSTANCE BUSY STATUS ACTIVE QUEUED RELEASE_COMMIT RESOURCES LAST_SEEN' ;;
    esac
    echo '--------------------------------------------------'
    row(){
      local label='' warning=''
      case "$MODE:$1" in
        enrolled:*|unenrolled:first|foreign-warning:first) label='yes' ;;
        unenrolled:second|foreign-warning:second) label='NO (not enrolled)'; warning="demo/$1" ;;
      esac
      if [[ "$MODE:$1" == 'foreign-warning:second' ]]; then warning='demo/other'; fi
      printf 'demo %s false idle 0 0 %s <unknown> 2026-01-01T00:00:00Z\n' "$1" "$label"
      if [[ -n "$warning" ]]; then echo "  NOT ENROLLED: $warning connected without a target enrollment token; it receives and can use no credentials."; fi
    }
    case "$MODE:$stage:$route" in
      denied:before:second) row "$identity"; exit 1 ;;
      empty:before:second) ;;
      ambiguous:before:second) row "$identity"; row other ;;
      missing:after:second) exit 1 ;;
      swapped:after:first) row second ;;
      swapped:after:second) row first ;;
      *) row "$identity" ;;
    esac
    echo 'ACTIVE/QUEUED count Gateway operations, not do-agent turn permits or prompt queues.'
    ''')
    fixture = tmp_path / "candidate"
    fixture.touch(mode=0o700)
    trace = tmp_path / "trace"
    commit = "a" * 40
    command = ["bash", str(DEPLOY), "--host", "fixture.invalid", "--no-build", "--binary", str(fixture),
               "--source-commit", commit, "--verify-session", "test"]
    for route in routes:
        command.extend(["--verify-target", route])
    result = subprocess.run(command, env={"PATH": f"{tmp_path}:{os.defpath}", "FIX": str(tmp_path),
                                         "TRACE": str(trace), "MODE": mode, "COMMIT": commit},
                            text=True, capture_output=True, timeout=12)
    events = trace.read_text().splitlines()
    assert events[:len(routes)] == ["before:" + route for route in routes]
    assert (result.returncode == 0) == (expected == "success"), (events, result.stderr)
    if expected == "success":
        assert events == [*("before:" + r for r in routes), "upload", *("after:" + r for r in routes)]
        assert f"Baseline targets: {len(routes)}" in result.stdout
        if mode == "unenrolled":
            assert "NOT ENROLLED: demo/second" in result.stderr
    elif expected == "baseline_failure":
        assert "upload" not in events
    else:
        assert "after:second" in events and events[-3:] == ["rollback", "restored:first", "restored:second"]


def test_gateway_deploy_enrollment_policy_flags_and_conflict():
    text = DEPLOY.read_text()

    assert "DOOPS_REQUIRE_AGENT_ENROLLMENT=1" in text
    assert "DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1" in text
    assert "if [[ \"${AGENT_REGISTRATION}\" != \"preserve\" || \"${UNENROLLED_CREDENTIALS}\" != \"preserve\" ]]; then" in text

    result = subprocess.run(
        [
            "bash", str(DEPLOY),
            "--host", "fixture.invalid",
            "--dry-run",
            "--require-agent-enrollment",
            "--allow-unenrolled-credential-delivery",
        ],
        text=True,
        capture_output=True,
        timeout=10,
    )
    assert result.returncode == 2
    assert "--allow-unenrolled-credential-delivery cannot be combined with --require-agent-enrollment" in result.stderr
