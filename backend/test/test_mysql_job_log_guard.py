"""The MySQL job log checker must fail on the logs it exists to reject.

INT-81 put the MySQL suite in CI, but its gate only looked at the go test exit
status and at top-level skip lines, so a log whose cases went back to skipping
(or a -run pattern that stopped matching) could still pass. The checker reads the
-v log instead; these cases pin both directions so the checker is not itself an
unverified black box.
"""

import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "check-mysql-test-log.py"
FIXTURES = Path(__file__).resolve().parent / "fixtures" / "mysql_job"

DROP_CASE = "TestGatewayMySQLInstallAndValidate"


def run_checker(log_path):
    return subprocess.run(
        [sys.executable, str(SCRIPT), str(log_path)],
        capture_output=True,
        text=True,
    )


def test_real_run_log_passes():
    result = run_checker(FIXTURES / "real-run.txt")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "11 live-server cases verified" in result.stdout


def test_log_without_a_server_fails():
    result = run_checker(FIXTURES / "no-server.txt")
    assert result.returncode == 1, result.stdout + result.stderr
    assert "skipped although DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT was set" in result.stderr


def test_absent_expected_case_fails(tmp_path):
    kept = [
        line
        for line in (FIXTURES / "real-run.txt").read_text().splitlines()
        if DROP_CASE not in line
    ]
    log = tmp_path / "absent.log"
    log.write_text("\n".join(kept) + "\n")
    result = run_checker(log)
    assert result.returncode == 1, result.stdout + result.stderr
    assert DROP_CASE + ": never ran" in result.stderr


def test_truncated_log_fails_the_floor(tmp_path):
    truncated = (FIXTURES / "real-run.txt").read_text().splitlines()[:40]
    log = tmp_path / "truncated.log"
    log.write_text("\n".join(truncated) + "\n")
    result = run_checker(log)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "top-level runs" in result.stderr


def test_unreadable_log_fails(tmp_path):
    result = run_checker(tmp_path / "absent.log")
    assert result.returncode == 1, result.stdout + result.stderr
    assert "cannot read the test log" in result.stderr
