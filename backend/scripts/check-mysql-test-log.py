#!/usr/bin/env python3
"""Prove that the CI MySQL job executed its gated tests, not merely exited 0.

A green "go test" is not evidence: every MySQL case in this repository skips when
DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT is unset, and a skip is reported as success.
INT-81 built the MySQL job for exactly that reason, but its first version only
checked the command status and the count of skip lines, so a log where the cases
quietly went back to skipping could still pass. This script reads the -v log and
fails on three independent facts:

  1. every name in EXPECTED_PASS produced a top-level "--- PASS";
  2. no top-level result is a skip (a set variable that stops reaching the
     server, a renamed case, or a restored t.Skip all show up here);
  3. the log holds at least MIN_RUN_LINES run lines and MIN_TOP_LEVEL_RUNS
     top-level runs, so a -run pattern that no longer matches (or a log that ends
     early) cannot look complete.

Usage: check-mysql-test-log.py <log-file>
"""

import re
import sys

# Measured, not guessed: run 45398559239 (commit 2c330861, mysql:8.4.13) listed 85
# cases for -run 'GatewayMySQL|GatewayCopy' and wrote 223 === RUN lines counting
# subtests (a local repeat at 13350 wrote 220 in 47.9s). Both floors sit below the
# measurement with room for ordinary test surgery, while a suite that stops running
# half of the gated cases cannot pass. The second floor stops one test with two
# hundred subtests from satisfying the first on its own.
MIN_RUN_LINES = 180
MIN_TOP_LEVEL_RUNS = 80

# Each name below skipped in a clean export at 2c330861 with the variable unset,
# and passed against a live mysql:8.4; the selection was checked case by case, not
# copied from memory. TestGatewayMySQLUpsertsRenderForBothDialects is deliberately
# absent: it passes without a server, so its PASS proves nothing about MySQL.
EXPECTED_PASS = (
    "TestGatewayMySQLSessionCollationMatchesDatabase",
    "TestGatewayMySQLStoreRejectsTableWithForeignCollation",
    "TestGatewayMySQLStoreRejectsDSNCollationOverride",
    "TestGatewayMySQLStoreRejectsColumnWithForeignCollation",
    "TestGatewayMySQLCopyCommandDryRunOnEmptyDestinationPredictsTheCutover",
    "TestGatewayMySQLCutoverRehearsal",
    "TestGatewayMySQLHashColumnsAcceptIssuedHashes",
    "TestGatewayMySQLInstallAndValidate",
    "TestGatewayMySQLNumberedParametersBindByTextOrder",
    "TestGatewayMySQLTableExistsProbeExecutes",
    "TestGatewayMySQLRuntimeStartup",
)

RUN_RE = re.compile(r"^=== RUN\s+(\S+)$")
RESULT_RE = re.compile(r"^(\s*)--- (PASS|FAIL|SKIP): (\S+) \(")


def parse(path):
    runs = []
    results = {}
    with open(path, "r", encoding="utf-8", errors="replace") as handle:
        for line in handle:
            line = line.rstrip("\n")
            match = RUN_RE.match(line)
            if match:
                runs.append(match.group(1))
                continue
            match = RESULT_RE.match(line)
            if not match:
                continue
            indent, status, name = match.groups()
            if indent:
                # A subtest result says nothing about whether the gated test ran.
                continue
            results[name] = status
    return runs, results


def main(argv):
    if len(argv) != 1:
        print("usage: check-mysql-test-log.py <log-file>", file=sys.stderr)
        return 2
    path = argv[0]
    try:
        runs, results = parse(path)
    except OSError as error:
        print("::error::cannot read the test log " + path + ": " + str(error), file=sys.stderr)
        return 1

    top_level_runs = [name for name in runs if "/" not in name]
    passed = sorted(name for name, status in results.items() if status == "PASS")
    skipped = sorted(name for name, status in results.items() if status == "SKIP")
    failed = sorted(name for name, status in results.items() if status == "FAIL")

    problems = []
    for name in EXPECTED_PASS:
        status = results.get(name)
        if status is None:
            problems.append(name + ": never ran (no top-level result line in the log)")
        elif status != "PASS":
            problems.append(name + ": reported " + status + " instead of PASS")
    if skipped:
        problems.append(
            str(len(skipped))
            + " top-level test(s) skipped although DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT was set: "
            + ", ".join(skipped)
        )
    if failed:
        problems.append(str(len(failed)) + " top-level test(s) failed: " + ", ".join(failed))
    if len(runs) < MIN_RUN_LINES:
        problems.append(
            "only "
            + str(len(runs))
            + " run lines; this gate requires at least "
            + str(MIN_RUN_LINES)
            + " (the -run selection stopped matching, or the log is truncated)"
        )
    if len(top_level_runs) < MIN_TOP_LEVEL_RUNS:
        problems.append(
            "only "
            + str(len(top_level_runs))
            + " top-level runs; this gate requires at least "
            + str(MIN_TOP_LEVEL_RUNS)
            + " (the -run selection stopped matching, or the log is truncated)"
        )

    print(
        "checked "
        + path
        + ": runs="
        + str(len(top_level_runs))
        + " passed="
        + str(len(passed))
        + " skipped="
        + str(len(skipped))
        + " failed="
        + str(len(failed))
        + ", "
        + str(len(EXPECTED_PASS))
        + " live-server cases verified"
    )
    if problems:
        for problem in problems:
            print("::error::" + problem, file=sys.stderr)
        print("mysql job log check FAILED: " + str(len(problems)) + " problem(s)", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
