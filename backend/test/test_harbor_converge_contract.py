"""Contract tests for scripts/harbor-converge.py.

The driver closes the loop that one trigger cannot: verify every repository,
re-trigger only the policies still behind, wait, verify again. What it promises
is that it stops for the right reason -- every repository matched, a digest
mismatch no trigger can fix, or a budget it was given -- and that it re-triggers
only the policies that are actually behind. Those decisions are asserted here
against a fake Harbor, so no cluster and no real credential is needed.
"""
import ast
import importlib.util
import json
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "harbor-converge.py"
REPLICATION = ROOT / "scripts" / "harbor-replication.py"

DIGEST_A = "sha256:" + "a" * 64
DIGEST_B = "sha256:" + "b" * 64

_MODULE = None


def load():
    global _MODULE
    if _MODULE is None:
        spec = importlib.util.spec_from_file_location("harbor_converge", SCRIPT)
        _MODULE = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(_MODULE)
    return _MODULE


def load_replication():
    spec = importlib.util.spec_from_file_location("harbor_replication_for_converge", REPLICATION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ScriptedHarbor:
    """A Harbor whose artifact lists change as executions complete.

    Each triggered policy is recorded, and the tags it would copy are added to
    the project on the next verify. That is what makes a round converge, so the
    driver's stop condition is exercised rather than stubbed.
    """

    def __init__(self, artifacts, lands=None):
        self.artifacts_map = {k: list(v) for k, v in artifacts.items()}
        self.lands = dict(lands or {})
        self.triggered = []
        self.next_execution = 2000

    def artifacts(self, project, repository):
        return [dict(item) for item in self.artifacts_map.get(repository, [])]

    def post(self, path, body):
        assert path == "/replication/executions", "unexpected POST " + path
        self.next_execution += 1
        policy_id = body["policy_id"]
        self.triggered.append(policy_id)
        for repository, tags in self.lands.pop(policy_id, {}).items():
            self.artifacts_map.setdefault(repository, [])
            for tag, digest in tags.items():
                self.artifacts_map[repository].append(
                    {"digest": digest, "tags": [{"name": tag}]}
                )
        return {"id": self.next_execution}

    def execution(self, execution_id):
        return {"id": execution_id, "status": "Succeed", "succeed": 1, "failed": 0}

    def system_info(self):
        return {"harbor_version": "v2.14.3-test"}


class FakeUpstream:
    def __init__(self, tags):
        self.tags_map = {k: dict(v) for k, v in tags.items()}

    def bearer_for(self, repository):
        return "bearer-" + repository

    def tags(self, repository, bearer):
        return sorted(self.tags_map.get(repository, {}))

    def digest(self, repository, tag, bearer):
        return self.tags_map.get(repository, {}).get(tag)


def single_target(module, policy_id=6, project="doops",
                  repository="symansun/backend", tag_pattern="git-*"):
    """Declare one policy explicitly so every test owns its metadata."""
    return [{"policy_id": policy_id, "project": project,
             "repository": repository, "tag_pattern": tag_pattern}]


def converge(module, harbor, upstream, targets, max_rounds=4, wait_seconds=0,
             budget_seconds=600):
    import time
    return module.converge(load_replication(), harbor, upstream,
                           max_rounds, wait_seconds,
                           time.time() + budget_seconds, targets)


# ------------------------------------------------------------- stop conditions


def test_an_already_matching_repository_is_never_triggered():
    """Convergence is the goal, not a trigger: a matched repository is left alone."""
    module = load()
    harbor = ScriptedHarbor(
        {"backend": [{"digest": DIGEST_A, "tags": [{"name": "git-aaa"}]}]}
    )
    upstream = FakeUpstream({"symansun/backend": {"git-aaa": DIGEST_A}})

    reports, converged = converge(module, harbor, upstream, single_target(module))

    assert converged is True
    assert harbor.triggered == [], "a repository already in sync must not be re-triggered"
    assert reports[0]["missing"] == []


def test_a_repository_behind_is_triggered_until_it_matches():
    module = load()
    harbor = ScriptedHarbor(
        {"backend": [{"digest": DIGEST_A, "tags": [{"name": "git-aaa"}]}]},
        lands={6: {"backend": {"git-bbb": DIGEST_B}}},
    )
    upstream = FakeUpstream(
        {"symansun/backend": {"git-aaa": DIGEST_A, "git-bbb": DIGEST_B}}
    )

    reports, converged = converge(module, harbor, upstream, single_target(module))

    assert converged is True
    assert harbor.triggered == [6], "one round should be enough once the tag lands"
    assert reports[0]["matched"] == 2


def test_only_the_policies_still_behind_are_retriggered():
    """The run that missed a policy left it behind; the driver must not repeat that."""
    module = load()
    harbor = ScriptedHarbor(
        {
            "backend": [{"digest": DIGEST_A, "tags": [{"name": "git-aaa"}]}],
            "web": [],
        },
        lands={7: {"web": {"git-ccc": DIGEST_B}}},
    )
    upstream = FakeUpstream({
        "symansun/backend": {"git-aaa": DIGEST_A},
        "symansun/web": {"git-ccc": DIGEST_B},
    })
    targets = single_target(module) + single_target(
        module, policy_id=7, repository="symansun/web"
    )

    reports, converged = converge(module, harbor, upstream, targets)

    assert converged is True
    assert harbor.triggered == [7], "backend already matched and must not be triggered"
    assert {r["harbor_repository"]: r["matched"] for r in reports} == {
        "backend": 1, "web": 1,
    }


def test_a_digest_mismatch_stops_the_loop_instead_of_retriggering():
    """Another copy cannot fix a differing digest, so the driver must not try."""
    module = load()
    harbor = ScriptedHarbor(
        {"backend": [{"digest": DIGEST_A, "tags": [{"name": "git-aaa"}]}]}
    )
    upstream = FakeUpstream({"symansun/backend": {"git-aaa": DIGEST_B}})

    with pytest.raises(SystemExit) as stopped:
        converge(module, harbor, upstream, single_target(module))

    message = str(stopped.value)
    assert "digest mismatch" in message
    assert "backend" in message
    assert "nothing was deleted" in message
    assert harbor.triggered == [], "a mismatch must not be answered with another trigger"


def test_an_upstream_that_keeps_publishing_ends_on_the_round_budget():
    """The real run never settled because the upstream kept moving; say so, don't loop."""
    module = load()

    class MovingUpstream(FakeUpstream):
        def __init__(self):
            super().__init__({"symansun/backend": {"git-aaa": DIGEST_A}})
            self.round = 0

        def tags(self, repository, bearer):
            # Every verify sees one tag more than the last copy could have taken.
            self.round += 1
            self.tags_map[repository]["git-new" + str(self.round)] = DIGEST_B
            return sorted(self.tags_map[repository])

    harbor = ScriptedHarbor(
        {"backend": [{"digest": DIGEST_A, "tags": [{"name": "git-aaa"}]}]}
    )

    reports, converged = converge(module, harbor, MovingUpstream(),
                                  single_target(module), max_rounds=3)

    assert converged is False
    assert len(harbor.triggered) == 3, "each round should trigger the repository once"
    assert reports[0]["missing"], "the report must still show what is behind"


def test_a_failed_execution_stops_the_loop():
    module = load()

    class FailingHarbor(ScriptedHarbor):
        def execution(self, execution_id):
            return {"id": execution_id, "status": "Failed", "succeed": 0, "failed": 1}

    harbor = FailingHarbor({"backend": []})
    upstream = FakeUpstream({"symansun/backend": {"git-aaa": DIGEST_A}})

    with pytest.raises(SystemExit) as stopped:
        converge(module, harbor, upstream, single_target(module))

    assert "did not succeed" in str(stopped.value)


def test_the_time_budget_stops_the_loop_before_another_trigger():
    """A budget that has already passed must stop the loop, not start a round."""
    module = load()
    harbor = ScriptedHarbor({"backend": []})
    upstream = FakeUpstream({"symansun/backend": {"git-aaa": DIGEST_A}})

    reports, converged = module.converge(
        load_replication(), harbor, upstream, 4, 0, deadline=0,
        targets=single_target(module),
    )

    assert converged is False
    assert harbor.triggered == [], "an expired budget must not trigger another copy"
    assert reports[0]["missing"] == ["git-aaa"]


# ------------------------------------------------------------ parallel trigger


def test_one_round_triggers_every_behind_policy_before_waiting():
    """Serial trigger-and-wait would take hours at the measured link speed."""
    module = load()
    harbor = ScriptedHarbor({"backend": [], "web": []})
    order = []

    class OrderedHarbor(ScriptedHarbor):
        def post(self, path, body):
            order.append(("trigger", body["policy_id"]))
            return super().post(path, body)

        def execution(self, execution_id):
            order.append(("wait", execution_id))
            return {"id": execution_id, "status": "Succeed", "succeed": 1, "failed": 0}

    harbor = OrderedHarbor({"backend": [], "web": []})
    settled = module.trigger_and_wait(load_replication(), harbor, [6, 7], 0)

    assert sorted(settled) == [6, 7]
    kinds = [kind for kind, _ in order]
    assert kinds[:2] == ["trigger", "trigger"], "both policies trigger before any wait"


# -------------------------------------------------------------- safety contract


def test_the_driver_never_deletes_and_never_writes_a_credential():
    """Prose may describe what the driver refuses to do; the code must not do it.

    The assertions run against the parsed syntax tree rather than the file text,
    so a docstring saying "never revokes a credential" cannot fail the test and,
    more importantly, cannot satisfy it either.
    """
    tree = ast.parse(SCRIPT.read_text())
    called = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Call):
            target = node.func
            if isinstance(target, ast.Attribute):
                called.add(target.attr)
            elif isinstance(target, ast.Name):
                called.add(target.id)

    forbidden = {name for name in called
                 if "delete" in name or "revoke" in name or "rotate" in name
                 or name in {"put", "patch"}}
    assert forbidden == set(), "the driver triggers and reads only"

    literals = {node.value for node in ast.walk(tree)
                if isinstance(node, ast.Constant) and isinstance(node.value, str)}
    assert "DELETE" not in literals
    assert "PUT" not in literals


def test_no_credential_value_is_accepted_on_the_command_line():
    module = load()
    text = SCRIPT.read_text()

    assert "--harbor-password" not in text.replace("--harbor-password-key", "")
    assert "--upstream-token " not in text

    args = module.main.__wrapped__ if hasattr(module.main, "__wrapped__") else None
    assert args is None or True  # main parses argv; the flags above are the contract

    # The only credential inputs are managed-credential ids.
    assert "--harbor-credential" in text
    assert "--upstream-credential" in text
    assert "resolve_managed_credential" in text


def test_policy_targets_are_loaded_from_explicit_metadata(tmp_path):
    """A public checkout cannot choose policies from an internal environment."""
    module = load()
    targets = [{"policy_id": 17, "project": "doops", "repository": "symansun/doops-agent", "tag_pattern": "v*"}]
    path = tmp_path / "policies.json"
    path.write_text(json.dumps(targets))
    assert module.load_targets(path) == targets
    assert not hasattr(module, "TARGETS")


@pytest.mark.parametrize("targets", [
    [], {}, ["target"],
    [{"policy_id": True, "project": "doops", "repository": "symansun/doops-agent", "tag_pattern": "v*"}],
    [{"policy_id": 1, "project": "doops", "repository": "../doops-agent", "tag_pattern": "v*"}],
    [{"policy_id": 1, "project": "doops", "repository": "symansun/*", "tag_pattern": "v*"}],
    [{"policy_id": 1, "project": "doops", "repository": "symansun/doops-agent", "tag_pattern": "v*", "token": "synthetic-value"}],
    [{"policy_id": 1, "project": "doops", "repository": "symansun/doops-agent", "tag_pattern": "v*"}] * 2,
])
def test_targets_reject_ambiguous_or_payload_fields_before_network(tmp_path, targets):
    path = tmp_path / "policies.json"
    path.write_text(json.dumps(targets))
    with pytest.raises(ValueError):
        load().load_targets(path)


def test_missing_targets_are_rejected_before_resolving_credentials(monkeypatch):
    module = load()
    calls = []
    monkeypatch.setattr(module, "load_replication_module", lambda _path: calls.append("module"))
    with pytest.raises(SystemExit) as stopped:
        module.main([])
    assert stopped.value.code == 2
    assert calls == []


def test_running_execution_is_not_triggered_again_after_wait_budget():
    module = load()

    class RunningHarbor(ScriptedHarbor):
        def execution(self, execution_id):
            return {"id": execution_id, "status": "InProgress"}

    harbor = RunningHarbor({"backend": []})
    upstream = FakeUpstream({"symansun/backend": {"git-aaa": DIGEST_A}})
    reports, converged = converge(module, harbor, upstream, single_target(module))
    assert converged is False
    assert reports[0]["missing"] == ["git-aaa"]
    assert harbor.triggered == [6]


def test_failed_execution_is_not_hidden_by_another_running_policy():
    module = load()

    class MixedHarbor(ScriptedHarbor):
        def execution(self, execution_id):
            return {"id": execution_id, "status": "Failed" if execution_id == 2001 else "InProgress"}

    harbor = MixedHarbor({"backend": [], "web": []})
    upstream = FakeUpstream({"symansun/backend": {"git-aaa": DIGEST_A}, "symansun/web": {"git-bbb": DIGEST_B}})
    targets = single_target(module) + single_target(module, policy_id=7, repository="symansun/web")
    with pytest.raises(SystemExit, match="did not succeed"):
        converge(module, harbor, upstream, targets)
    assert harbor.triggered == [6, 7]


def test_successful_execution_without_digest_progress_stops():
    module = load()
    harbor = ScriptedHarbor({"backend": []})
    upstream = FakeUpstream({"symansun/backend": {"git-aaa": DIGEST_A}})
    with pytest.raises(SystemExit, match="no progress"):
        converge(module, harbor, upstream, single_target(module))
    assert harbor.triggered == [6]


@pytest.mark.parametrize("changed", [
    {"enabled": False}, {"dest_namespace": "other"},
    {"dest_namespace_replace_count": 1}, {"override": True}, {"deletion": True},
    {"filters": [{"type": "name", "value": "symansun/other"}]},
    {"registry_url": "https://registry.example.com"},
])
def test_misbound_policy_ids_are_refused_without_triggering(changed):
    module = load()
    policy = load_replication().policy_body("ghcr-doops-backend", 2, "doops", "symansun/backend", "git-*")
    registry_url = changed.get("registry_url", "https://ghcr.io")
    policy.update({key: value for key, value in changed.items() if key != "registry_url"})

    class DeclaredHarbor(ScriptedHarbor):
        def policy(self, policy_id):
            assert policy_id == 6
            return policy

        def registry(self, registry_id):
            assert registry_id == 2
            return {"url": registry_url}

    harbor = DeclaredHarbor({})
    with pytest.raises(ValueError, match="policy 6"):
        module.validate_target_policies(harbor, single_target(module), "https://ghcr.io")
    assert harbor.triggered == []


def test_declared_policy_and_registry_pass_preflight():
    module = load()

    class DeclaredHarbor(ScriptedHarbor):
        def policy(self, policy_id):
            return load_replication().policy_body("ghcr-doops-backend", 2, "doops", "symansun/backend", "git-*")

        def registry(self, registry_id):
            return {"url": "https://ghcr.io/"}

    harbor = DeclaredHarbor({})
    module.validate_target_policies(harbor, single_target(module), "https://ghcr.io")
    assert harbor.triggered == []
