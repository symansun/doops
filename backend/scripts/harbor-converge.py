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
from pathlib import Path
import re
import sys
import time


def load_targets(path):
    """Load metadata-only policy declarations; never infer a cluster inventory."""
    with open(path, encoding="utf-8") as source:
        targets = json.load(source)
    if not isinstance(targets, list) or not targets:
        raise ValueError("targets must be a non-empty JSON array")
    policy_ids = set()
    destinations = set()
    for target in targets:
        if not isinstance(target, dict) or set(target) != {
            "policy_id", "project", "repository", "tag_pattern"
        }:
            raise ValueError("each target requires only policy_id, project, repository and tag_pattern")
        if type(target["policy_id"]) is not int or target["policy_id"] <= 0:
            raise ValueError("policy_id must be a positive integer")
        project, repository, pattern = (target[key] for key in ("project", "repository", "tag_pattern"))
        if not isinstance(project, str) or not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", project):
            raise ValueError("project must be a Harbor project name")
        if not isinstance(repository, str) or not re.fullmatch(r"[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)+", repository):
            raise ValueError("repository must be an exact registry repository path")
        if not isinstance(pattern, str) or not re.fullmatch(r"[A-Za-z0-9_.-]*\*|[A-Za-z0-9_][A-Za-z0-9_.-]*", pattern):
            raise ValueError("tag_pattern must be an exact tag or a prefix ending in '*'")
        destination = (project, repository.rsplit("/", 1)[-1])
        if target["policy_id"] in policy_ids or destination in destinations:
            raise ValueError("targets contain a duplicate policy id or Harbor destination")
        policy_ids.add(target["policy_id"])
        destinations.add(destination)
    return targets


def load_replication_module(path):
    spec = importlib.util.spec_from_file_location("harbor_replication", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def validate_target_policies(client, targets, registry_url):
    """A file's id is not authority to trigger an unrelated Harbor policy."""
    for target in targets:
        policy = client.policy(target["policy_id"])
        filters = {item.get("type"): item.get("value") for item in (policy.get("filters") or [])}
        if not (
            policy.get("enabled") is True
            and policy.get("dest_namespace") == target["project"]
            and policy.get("dest_namespace_replace_count") == -1
            and policy.get("override") is False
            and policy.get("deletion") is False
            and filters.get("name") == target["repository"]
            and filters.get("tag") == target["tag_pattern"]
        ):
            raise ValueError("policy " + str(target["policy_id"]) + " does not match its target declaration")
        registry_id = (policy.get("src_registry") or {}).get("id")
        registry = client.registry(registry_id)
        if registry.get("url", "").rstrip("/") != registry_url.rstrip("/"):
            raise ValueError("policy " + str(target["policy_id"]) + " uses a different registry endpoint")


def log(message):
    print(message, flush=True)


def verify_all(module, client, upstream, targets):
    """Verify every target once and return one report per repository."""
    reports = []
    for target in targets:
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

    The executions are independent, so trigger all selected policies before
    waiting. An unfinished execution is returned explicitly and must not be
    duplicated by a later round.
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
        if time.time() >= deadline:
            log("    still running after the wait budget: "
                + ", ".join(str(e) for e in sorted(executions.values())))
            for policy_id, execution_id in executions.items():
                settled[policy_id] = {"execution_id": execution_id, "status": "InProgress"}
            break
        time.sleep(min(30, max(0, deadline - time.time())))
    return settled


def converge(module, client, upstream, max_rounds, wait_seconds, deadline, targets):
    previous_missing = None
    for round_number in range(1, max_rounds + 1):
        log("=== round " + str(round_number) + " verify ===")
        reports = verify_all(module, client, upstream, targets)

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

        missing = tuple((r["policy_id"], tuple(r["missing"])) for r in behind)
        if missing == previous_missing:
            raise SystemExit("harbor_converge=failed no progress after completed replication; inspect the policy tasks before retrying")
        previous_missing = missing

        log("=== round " + str(round_number) + " trigger "
            + ", ".join(r["harbor_repository"] + "(" + str(len(r["missing"])) + ")"
                        for r in behind) + " ===")
        if time.time() >= deadline:
            log("harbor_converge=incomplete the time budget ran out with "
                + str(len(behind)) + " repository/repositories behind")
            return reports, False

        settled = trigger_and_wait(
            module, client, [r["policy_id"] for r in behind],
            min(wait_seconds, max(0, deadline - time.time())),
        )
        failed = [str(p) for p, s in settled.items() if s["status"] not in {"Succeed", "InProgress"}]
        if failed:
            raise SystemExit(
                "harbor_converge=failed execution for policy " + ", ".join(failed)
                + " did not succeed"
            )
        if any(s["status"] == "InProgress" for s in settled.values()):
            log("harbor_converge=incomplete execution still running; inspect its tasks before retrying")
            return reports, False

    log("harbor_converge=incomplete the round budget ran out")
    return verify_all(module, client, upstream, targets), False


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--targets-file", required=True,
                        help="JSON array of policy_id/project/repository/tag_pattern declarations")
    parser.add_argument("--script", default=str(Path(__file__).with_name("harbor-replication.py")),
                        help="Path to harbor-replication.py (default: %(default)s)")
    parser.add_argument("--harbor-url", required=True)
    parser.add_argument("--harbor-credential", required=True,
                        help="Managed credential id holding the Harbor password")
    parser.add_argument("--upstream-url", default="https://ghcr.io")
    parser.add_argument("--upstream-credential", required=True,
                        help="Managed credential id holding the upstream token")
    parser.add_argument("--credential-namespace", required=True)
    parser.add_argument("--max-rounds", type=int, default=8,
                        help="Give up after this many verify/trigger rounds (default: %(default)s)")
    parser.add_argument("--wait-seconds", type=int, default=2400,
                        help="How long to wait for one round's executions (default: %(default)s)")
    parser.add_argument("--budget-seconds", type=int, default=10800,
                        help="Stop starting new rounds after this long (default: %(default)s)")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)

    if args.max_rounds < 1 or args.wait_seconds < 1 or args.budget_seconds < 1:
        parser.error("round, wait and time budgets must be positive")
    targets = load_targets(args.targets_file)

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
    upstream_user = module.require_key(upstream_values, "GITHUB_USERNAME", "upstream credential")
    upstream_token = module.require_key(upstream_values, "GITHUB_TOKEN", "upstream credential")

    client = module.HarborClient(args.harbor_url, harbor_user, harbor_password)
    info = client.system_info()
    log("harbor " + str(info.get("harbor_version", "unknown")) + " reachable")
    validate_target_policies(client, targets, args.upstream_url)
    upstream = module.UpstreamRegistry(args.upstream_url, upstream_user, upstream_token)

    reports, converged = converge(
        module, client, upstream,
        args.max_rounds, args.wait_seconds, time.time() + args.budget_seconds,
        targets,
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
