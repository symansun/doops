# MySQL job log fixtures

Inputs for backend/scripts/check-mysql-test-log.py, referenced by
backend/test/test_mysql_job_log_guard.py.

- real-run.txt: `go test -run 'GatewayMySQL|GatewayCopy' -count=1 -v -skip
  'TestGatewayMySQLCredentialLifecycleE2E|TestGatewayMySQLCredentialPromoteRequiresEveryActiveGrant'`
  from a clean export of b2369db9 against a local mysql:8.4.13 container
  (`--character-set-server=utf8mb4 --collation-server=utf8mb4_bin`, published on
  127.0.0.1:13350) at 2026-11-24 21:16 +08:00. 220 run lines, 83 top-level PASS,
  no FAIL. The two skipped cases are excluded because they need a verify call
  faster than the suite's 3s HTTP timeout, which a macOS host misses on first
  execution (0.81s just to exec the freshly written fake kubectl); they pass on
  the Linux runner (run 45398559239).
- no-server.txt: the same selection in the same tree with
  DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT unset: 66 top-level SKIP, the rest PASS,
  exit status 0 — the shape a green-but-useless run has.

Both were edited only by the test command that produced them; do not hand-patch
them, re-capture instead so the fixtures keep proving the real output shape.
