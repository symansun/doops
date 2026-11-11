"""Drive Harbor replication until every repository matches its upstream.

One trigger copies only the tags the upstream served at trigger time, so a
repository whose upstream keeps publishing needs more than one round. This
driver closes that loop: verify every repository, re-trigger only the policies
that still report a missing tag, wait for those executions, and verify again.

It stops when every repository reports missing=0 in the same round, or when the
round or time budget runs out. It triggers and reads; it never deletes a Harbor
object and never writes, revokes or rotates a credential. Credential values are
read from the materialized Secret inside the cluster and stay in memory.

Run it on the Agent host, next to harbor-replication.py, whose client and
verification code it reuses.
"""
import argparse
import importlib.util
import json
import os
import sys
import time

# Each entry is one replication policy: its Harbor project, its id, and the
# upstream repository it pulls. The ids are the live policies; the driver never
# creates one, so an id it cannot read is an error rather than a new policy.
TARGETS = [
    {"policy_id": 6, "project": "agentcloud", "repository": "l8ai/agentcloud/backend",
     "tag_pattern": "git-*"},
    {"policy_id": 7, "project": "agentcloud", "repository": "l8ai/agentcloud/web",
     "tag_pattern": "git-*"},
    {"policy_id": 8, "project": "agentcloud", "repository": "l8ai/agentcloud/relay",
     "tag_pattern": "git-*"},
    {"policy_id": 9, "project": "zhiyong",
     "repository": "example/zhiyong/education/zhiyong-frontend", "tag_pattern": "sha-*"},
    {"policy_id": 10, "project": "zhiyong",
     "repository": "example/zhiyong/education/zhiyong-lab-api", "tag_pattern": "sha-*"},
    {"policy_id": 11, "project": "zhiyong",
     "repository": "example/zhiyong/education/zhiyong-course-api", "tag_pattern": "sha-*"},
    {"policy_id": 12, "project": "zhiyong",
     "repository": "example/zhiyong/education/zhiyong-ai-api", "tag_pattern": "sha-*"},
    {"policy_id": 13, "project": "zhiyong",
     "repository": "example/zhiyong/education/zhiyong-exam-api", "tag_pattern": "sha-*"},
]


def load_replication_module(path):
    spec = importlib.util.spec_from_file_location("harbor_replication", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def log(message):
    print(message, flush=True)


def verify_all(module, client, upstream):
    """Verify every target once and return one report per repository."""
    reports = []
    for target in TARGETS:
        report = module.verify_repository(
            client, target["project"],
            module.harbor_repository_for(target["repository"]),
            upstream, target["repository"], target["tag_pattern"],
        )
        report["policy_id"] = target["policy_id"]
        reports.append(report)
        log("    " + report["harbor_repository"]
            + " upstream=" + str(report["upstream_tags"])
            + " matched=" + str(report["matched"])
            + " mismatched=" + str(len(report["mismatched"]))
            + " missing=" + str(len(report["missing"]))
            + " extra=" + str(len(report["extra"])))
        for item in report["mismatched"]:
            log("      MISMATCH " + item["tag"]
                + " harbor=" + item["harbor"] + " upstream=" + item["upstream"])
    return reports


def trigger_and_wait(module, client, policy_ids, timeout_seconds):
    """Trigger every policy at once, then wait for all of them.

    Triggering in parallel matters: the copies run at tens of KB/s, so starting
    them one after another would add up to hours. The executions are
    independent, so waiting for all of them afterwards is enough.
    """
    executions = {}
    for policy_id in policy_ids:
        execution_id = module.trigger_replication(client, policy_id)
        executions[policy_id] = execution_id

    settled = {}
    deadline = time.time() + timeout_seconds
    while executions:
        for policy_id, execution_id in sorted(executions.items()):
            state = client.execution(execution_id)
            status = state.get("status")
            if status != "InProgress":
                settled[policy_id] = {"execution_id": execution_id, "status": status,
                                      "succeed": state.get("succeed"),
                                      "failed": state.get("failed")}
                log("    execution " + str(execution_id) + " policy " + str(policy_id)
                    + " -> " + str(status) + " ok=" + str(state.get("succeed"))
                    + " fail=" + str(state.get("failed")))
        for policy_id in settled:
            executions.pop(policy_id, None)
        if not executions:
            break
        if time.time() > deadline:
            # A slow copy is not a failed copy. Report what is still running and
            # let the next round pick it up rather than declaring failure.
            log("    still running after the wait budget: "
                + ", ".join(str(e) for e in sorted(executions.values())))
            break
        time.sleep(30)
    return settled


def converge(module, client, upstream, max_rounds, wait_seconds, deadline):
    for round_number in range(1, max_rounds + 1):
        log("=== round " + str(round_number) + " verify ===")
        reports = verify_all(module, client, upstream)

        mismatched = [r for r in reports if r["mismatched"]]
        if mismatched:
            # A digest difference is not something another trigger can fix.
            raise SystemExit(
                "harbor_converge=failed digest mismatch in "
                + ", ".join(r["harbor_repository"] for r in mismatched)
                + "; nothing was deleted"
            )

        behind = [r for r in reports if r["missing"]]
        if not behind:
            log("harbor_converge=complete all " + str(len(reports))
                + " repositories match their upstream")
            return reports, True

        log("=== round " + str(round_number) + " trigger "
            + ", ".join(r["harbor_repository"] + "(" + str(len(r["missing"])) + ")"
                        for r in behind) + " ===")
        if time.time() > deadline:
            log("harbor_converge=incomplete the time budget ran out with "
                + str(len(behind)) + " repository/repositories behind")
            return reports, False

        settled = trigger_and_wait(
            module, client, [r["policy_id"] for r in behind], wait_seconds
        )
        failed = [str(p) for p, s in settled.items() if s["status"] != "Succeed"]
        if failed:
            raise SystemExit(
                "harbor_converge=failed execution for policy " + ", ".join(failed)
                + " did not succeed"
            )

    log("harbor_converge=incomplete the round budget ran out")
    return verify_all(module, client, upstream), False


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--script", default="/root/ws/harbor-script/harbor-replication.py",
                        help="Path to harbor-replication.py (default: %(default)s)")
    parser.add_argument("--harbor-url", default="http://harbor-core.harbor-system.svc")
    parser.add_argument("--harbor-credential", required=True,
                        help="Managed credential id holding the Harbor password")
    parser.add_argument("--upstream-url", required=True,
                        help="Upstream registry URL, for example https://registry.example.com")
    parser.add_argument("--upstream-credential", required=True,
                        help="Managed credential id holding the upstream token")
    parser.add_argument("--credential-namespace", default="harbor-system")
    parser.add_argument("--max-rounds", type=int, default=8,
                        help="Give up after this many verify/trigger rounds (default: %(default)s)")
    parser.add_argument("--wait-seconds", type=int, default=2400,
                        help="How long to wait for one round's executions (default: %(default)s)")
    parser.add_argument("--budget-seconds", type=int, default=10800,
                        help="Stop starting new rounds after this long (default: %(default)s)")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)

    if not os.path.exists(args.script):
        raise SystemExit("harbor_converge=failed cannot find " + args.script)
    module = load_replication_module(args.script)

    harbor_values = module.resolve_managed_credential(
        args.harbor_credential, args.credential_namespace
    )
    upstream_values = module.resolve_managed_credential(
        args.upstream_credential, args.credential_namespace
    )
    harbor_user = harbor_values.get("HARBOR_ADMIN_USERNAME") or "admin"
    harbor_password = module.require_key(
        harbor_values, "HARBOR_ADMIN_PASSWORD", "Harbor credential"
    )
    upstream_user = module.require_key(upstream_values, "UPSTREAM_USERNAME", "upstream credential")
    upstream_token = module.require_key(upstream_values, "UPSTREAM_TOKEN", "upstream credential")

    client = module.HarborClient(args.harbor_url, harbor_user, harbor_password)
    info = client.system_info()
    log("harbor " + str(info.get("harbor_version", "unknown")) + " reachable")
    upstream = module.UpstreamRegistry(args.upstream_url, upstream_user, upstream_token)

    reports, converged = converge(
        module, client, upstream,
        args.max_rounds, args.wait_seconds, time.time() + args.budget_seconds,
    )
    if args.json:
        print(json.dumps({"converged": converged, "verifications": reports},
                         indent=2, sort_keys=True))
    return 0 if converged else 1


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except SystemExit:
        raise
    except Exception as failure:  # noqa: BLE001 - the message is the operator's next step
        print("harbor_converge=failed " + str(failure), file=sys.stderr)
        sys.exit(1)
