"""Contract tests for scripts/harbor-replication.py.

The script configures a Harbor to pull images from an upstream registry. Its two
promises are that a second run converges to the same objects instead of creating
duplicates, and that a credential payload never reaches the output. Both are
asserted here against a fake Harbor, so no cluster and no real credential is
needed. Payload values are synthetic canaries.
"""
import importlib.util
import json
import re
import urllib.error
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "harbor-replication.py"

HARBOR_PASSWORD_CANARY = "canary-harbor-password-must-not-appear"
UPSTREAM_TOKEN_CANARY = "canary-upstream-token-must-not-appear"
DIGEST_A = "sha256:" + "a" * 64
DIGEST_B = "sha256:" + "b" * 64

_MODULE = None


def load():
    global _MODULE
    if _MODULE is None:
        spec = importlib.util.spec_from_file_location("harbor_replication", SCRIPT)
        _MODULE = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(_MODULE)
    return _MODULE


class FakeHarbor:
    """A Harbor that records every write, with v2.14's real response quirks.

    Two quirks matter and are reproduced: creating a policy or a project returns
    a body without a usable id, so the script has to resolve the object by name;
    and a registry PUT is accepted even when the credential is not stored.
    """

    def __init__(self, registries=None, projects=None, policies=None,
                 immutable=None, artifacts=None, store_credential=True,
                 registry_status="healthy"):
        self.registries_list = list(registries or [])
        self.projects_list = list(projects or [])
        self.policies_list = list(policies or [])
        self.immutable_list = list(immutable or [])
        self.artifacts_map = dict(artifacts or {})
        self.store_credential = store_credential
        self.registry_status = registry_status
        self.posted = []
        self.puts = []
        self.next_id = 100

    # -- the surface the script actually calls -----------------------------

    def system_info(self):
        return {"harbor_version": "v2.14.3-test"}

    def registries(self):
        return [dict(item) for item in self.registries_list]

    def registry(self, registry_id):
        for item in self.registries_list:
            if item["id"] == registry_id:
                found = dict(item)
                found["status"] = self.registry_status
                return found
        raise AssertionError("registry " + str(registry_id) + " does not exist")

    def projects(self):
        return [dict(item) for item in self.projects_list]

    def policies(self):
        return [dict(item) for item in self.policies_list]

    def policy(self, policy_id):
        for item in self.policies_list:
            if item["id"] == policy_id:
                return dict(item)
        raise AssertionError("policy " + str(policy_id) + " does not exist")

    def immutable_rules(self, project):
        return [dict(item) for item in self.immutable_list]

    def artifacts(self, project, repository):
        return [dict(item) for item in self.artifacts_map.get(repository, [])]

    def execution(self, execution_id):
        return {"id": execution_id, "status": "Succeed", "succeed": 1, "failed": 0}

    def post(self, path, body):
        self.posted.append((path, body))
        self.next_id += 1
        if path == "/registries":
            entry = {"id": self.next_id, "name": body["name"], "url": body["url"],
                     "type": body["type"], "insecure": body["insecure"]}
            if self.store_credential:
                entry["credential"] = {"access_key": body["access_key"],
                                       "access_secret": "*****"}
            self.registries_list.append(entry)
            return {}
        if path == "/projects":
            self.projects_list.append(
                {"project_id": self.next_id, "name": body["project_name"]}
            )
            return {}
        if path == "/replication/policies":
            entry = dict(body)
            entry["id"] = self.next_id
            self.policies_list.append(entry)
            return {}
        if path.endswith("/immutabletagrules"):
            entry = dict(body)
            entry["id"] = self.next_id
            self.immutable_list.append(entry)
            return {}
        if path == "/replication/executions":
            return {"id": self.next_id}
        raise AssertionError("unexpected POST " + path)

    def put(self, path, body):
        self.puts.append((path, body))
        if path.startswith("/registries/"):
            registry_id = int(path.rsplit("/", 1)[1])
            for item in self.registries_list:
                if item["id"] == registry_id and self.store_credential:
                    item["credential"] = {"access_key": body["access_key"],
                                          "access_secret": "*****"}
            return {}
        if path.startswith("/replication/policies/"):
            policy_id = int(path.rsplit("/", 1)[1])
            for index, item in enumerate(self.policies_list):
                if item["id"] == policy_id:
                    updated = dict(body)
                    updated["id"] = policy_id
                    self.policies_list[index] = updated
            return {}
        raise AssertionError("unexpected PUT " + path)


class FakeUpstream:
    def __init__(self, tags):
        self.tags_map = dict(tags)

    def bearer_for(self, repository):
        return "bearer-" + repository

    def tags(self, repository, bearer):
        return sorted(self.tags_map.get(repository, {}))

    def digest(self, repository, tag, bearer):
        return self.tags_map.get(repository, {}).get(tag)


def args_for(**overrides):
    argv = [
        "run",
        "--harbor-url", "http://harbor-core.harbor-system.svc",
        "--harbor-credential", "cred_harbor",
        "--upstream-url", "https://upstream.example.com",
        "--upstream-credential", "cred_upstream",
        "--credential-namespace", "harbor-system",
        "--project", "agentcloud",
        "--repository", "l8ai/agentcloud/backend",
        "--tag-pattern", "git-*",
    ]
    for flag, value in overrides.items():
        argv.extend(["--" + flag.replace("_", "-"), value])
    return load().parse_args(argv)


def args_with_trigger():
    module = load()
    return module.parse_args([
        "run",
        "--harbor-url", "http://harbor-core.harbor-system.svc",
        "--harbor-credential", "cred_harbor",
        "--upstream-url", "https://upstream.example.com",
        "--upstream-credential", "cred_upstream",
        "--credential-namespace", "harbor-system",
        "--project", "agentcloud",
        "--repository", "l8ai/agentcloud/backend",
        "--tag-pattern", "git-*",
        "--trigger",
    ])


# ----------------------------------------------------------------- reuse/idempotence


def configured_harbor():
    """A Harbor that already holds everything the script would create."""
    return FakeHarbor(
        registries=[{"id": 2, "name": "cnb-upstream",
                     "url": "https://upstream.example.com", "type": "docker-registry",
                     "insecure": False,
                     "credential": {"access_key": "robot", "access_secret": "*****"}}],
        projects=[{"project_id": 226, "name": "agentcloud"}],
        policies=[{
            "id": 6, "name": "cnb-agentcloud-backend", "enabled": True,
            "dest_namespace": "agentcloud", "dest_namespace_replace_count": -1,
            "filters": [{"type": "name", "value": "l8ai/agentcloud/backend"},
                        {"type": "tag", "value": "git-*"}],
        }],
        immutable=[{"id": 38, "tag_selectors": [{"pattern": "git-*"}]}],
    )


def test_second_run_reuses_every_object_and_writes_nothing():
    """The whole point of the script: run it twice, get the same Harbor."""
    module = load()
    harbor = configured_harbor()

    result = module.run(harbor, args_for(), "robot", UPSTREAM_TOKEN_CANARY)

    assert result["registry"] == {"name": "cnb-upstream", "id": 2, "created": False}
    assert result["project"] == {"name": "agentcloud", "id": 226, "created": False}
    assert result["immutable_rule"] == {"id": 38, "created": False}
    assert result["policies"] == [{
        "repository": "l8ai/agentcloud/backend",
        "policy": "cnb-agentcloud-backend",
        "policy_id": 6,
        "created": False,
    }]
    assert harbor.posted == [], "a reused object must not be created again"
    assert harbor.puts == [], "a matching object must not be rewritten"


def test_first_run_creates_each_object_once_and_resolves_ids_by_name():
    module = load()
    harbor = FakeHarbor()

    result = module.run(harbor, args_for(), "robot", UPSTREAM_TOKEN_CANARY)

    created = [path for path, _ in harbor.posted]
    assert created == [
        "/registries",
        "/projects",
        "/projects/agentcloud/immutabletagrules",
        "/replication/policies",
    ]
    assert result["registry"]["created"] is True
    assert result["project"]["created"] is True
    assert result["immutable_rule"]["created"] is True
    assert result["policies"][0]["created"] is True
    # Harbor returned no usable id in any of those bodies; every id came from a
    # follow-up lookup by name, so none of them may be zero or missing.
    assert result["registry"]["id"] > 0
    assert result["project"]["id"] > 0
    assert result["policies"][0]["policy_id"] > 0


def test_policy_name_is_stable_for_every_configured_repository():
    """A second run must derive the same name, or it creates a duplicate policy."""
    module = load()
    cases = [
        ("agentcloud", "l8ai/agentcloud/backend", "cnb-agentcloud-backend"),
        ("agentcloud", "l8ai/agentcloud/web", "cnb-agentcloud-web"),
        ("agentcloud", "l8ai/agentcloud/relay", "cnb-agentcloud-relay"),
        ("zhiyong", "aiedulab/zhiyong/education/zhiyong-frontend", "cnb-zhiyong-frontend"),
        ("zhiyong", "aiedulab/zhiyong/education/zhiyong-lab-api", "cnb-zhiyong-lab-api"),
        ("zhiyong", "aiedulab/zhiyong/education/zhiyong-exam-api", "cnb-zhiyong-exam-api"),
    ]
    for project, repository, expected in cases:
        assert module.policy_name_for("cnb", project, repository) == expected


def test_existing_policy_with_other_filters_is_updated_not_duplicated():
    module = load()
    harbor = configured_harbor()
    harbor.policies_list[0]["filters"] = [
        {"type": "name", "value": "l8ai/agentcloud/backend"},
        {"type": "tag", "value": "release-*"},
    ]

    result = module.run(harbor, args_for(), "robot", UPSTREAM_TOKEN_CANARY)

    assert [path for path, _ in harbor.puts] == ["/replication/policies/6"]
    assert harbor.posted == [], "an existing policy must be updated, never re-created"
    assert result["policies"][0]["policy_id"] == 6


def test_pull_policy_names_only_the_source_registry():
    """Harbor rejects a body that fills in both registry sides."""
    module = load()
    body = module.policy_body("cnb-agentcloud-backend", 2, "agentcloud",
                              "l8ai/agentcloud/backend", "git-*")

    assert body["src_registry"] == {"id": 2}
    assert "dest_registry" not in body
    assert body["dest_namespace"] == "agentcloud"
    assert body["dest_namespace_replace_count"] == -1
    assert body["override"] is False
    assert body["trigger"]["type"] == "manual"


def test_project_is_created_without_a_registry_id():
    """registry_id 0 is rejected and a real id would make a proxy cache."""
    module = load()
    harbor = FakeHarbor()

    module.ensure_project(harbor, "agentcloud")

    path, body = harbor.posted[0]
    assert path == "/projects"
    assert "registry_id" not in body
    assert body["metadata"]["public"] == "false"


# --------------------------------------------------------------------- failures


def test_registry_endpoint_that_silently_drops_the_credential_fails_closed():
    """Harbor answers 200 on a PUT whose credential it did not store."""
    module = load()
    harbor = FakeHarbor(
        registries=[{"id": 2, "name": "cnb-upstream",
                     "url": "https://upstream.example.com",
                     "type": "docker-registry", "insecure": False}],
        store_credential=False,
    )

    with pytest.raises(module.HarborError) as failure:
        module.ensure_registry_endpoint(
            harbor, "cnb-upstream", "https://upstream.example.com",
            "robot", UPSTREAM_TOKEN_CANARY, False,
        )

    assert "did not store the credential" in str(failure.value)
    assert UPSTREAM_TOKEN_CANARY not in str(failure.value)


def test_unhealthy_registry_endpoint_stops_the_run():
    module = load()
    harbor = configured_harbor()
    harbor.registry_status = "unhealthy"

    with pytest.raises(module.HarborError) as failure:
        module.run(harbor, args_for(), "robot", UPSTREAM_TOKEN_CANARY)

    assert "not healthy" in str(failure.value)


def test_a_policy_that_will_not_take_the_requested_filters_fails():
    module = load()
    harbor = configured_harbor()
    harbor.policies_list[0]["filters"] = [{"type": "tag", "value": "release-*"}]

    class StubbornHarbor(type(harbor)):
        def put(self, path, body):
            self.puts.append((path, body))
            return {}

    harbor.__class__ = StubbornHarbor

    with pytest.raises(module.HarborError) as failure:
        module.run(harbor, args_for(), "robot", UPSTREAM_TOKEN_CANARY)

    assert "did not accept the requested filters" in str(failure.value)


# ------------------------------------------------------------------ verification


def verify(harbor, upstream, tag_pattern="git-*"):
    return load().verify_repository(
        harbor, "agentcloud", "backend", upstream, "l8ai/agentcloud/backend", tag_pattern
    )


def harbor_with_tags(tags):
    return FakeHarbor(artifacts={
        "backend": [{"digest": digest, "tags": [{"name": tag}]}
                    for tag, digest in tags.items()]
    })


def test_verification_passes_when_every_upstream_tag_matches():
    report = verify(
        harbor_with_tags({"git-aaa": DIGEST_A, "git-bbb": DIGEST_B}),
        FakeUpstream({"l8ai/agentcloud/backend": {"git-aaa": DIGEST_A, "git-bbb": DIGEST_B}}),
    )

    assert report["ok"] is True
    assert report["matched"] == 2
    assert report["mismatched"] == []
    assert report["missing"] == []
    assert report["extra"] == []


def test_an_upstream_tag_absent_from_harbor_fails_verification():
    """The failure that matters: a published tag the replication never copied."""
    report = verify(
        harbor_with_tags({"git-aaa": DIGEST_A}),
        FakeUpstream({"l8ai/agentcloud/backend": {"git-aaa": DIGEST_A, "git-new": DIGEST_B}}),
    )

    assert report["ok"] is False
    assert report["missing"] == ["git-new"]
    assert report["matched"] == 1


def test_a_differing_digest_fails_verification():
    report = verify(
        harbor_with_tags({"git-aaa": DIGEST_A}),
        FakeUpstream({"l8ai/agentcloud/backend": {"git-aaa": DIGEST_B}}),
    )

    assert report["ok"] is False
    assert report["mismatched"] == [
        {"tag": "git-aaa", "harbor": DIGEST_A, "upstream": DIGEST_B}
    ]


def test_a_tag_the_upstream_retired_is_reported_but_does_not_fail():
    """The upstream runs its own tag retention; a kept copy is not a fault."""
    report = verify(
        harbor_with_tags({"git-aaa": DIGEST_A, "git-old": DIGEST_B}),
        FakeUpstream({"l8ai/agentcloud/backend": {"git-aaa": DIGEST_A}}),
    )

    assert report["ok"] is True
    assert report["extra"] == ["git-old"]
    assert report["missing"] == []


def test_verification_ignores_tags_outside_the_pattern():
    report = verify(
        harbor_with_tags({"git-aaa": DIGEST_A, "latest": DIGEST_B}),
        FakeUpstream({"l8ai/agentcloud/backend": {"git-aaa": DIGEST_A, "latest": DIGEST_A}}),
    )

    assert report["ok"] is True
    assert report["upstream_tags"] == 1
    assert report["extra"] == []


def test_an_unreadable_upstream_tag_list_is_an_error_not_an_empty_result():
    """An upstream that cannot be listed must not read as "nothing to copy"."""
    module = load()

    class BrokenUpstream(FakeUpstream):
        def tags(self, repository, bearer):
            raise urllib.error.URLError("connection reset")

    with pytest.raises(module.UpstreamError) as failure:
        verify(harbor_with_tags({"git-aaa": DIGEST_A}),
               BrokenUpstream({"l8ai/agentcloud/backend": {}}))

    assert "cannot list upstream tags" in str(failure.value)


# ------------------------------------------------------------------ credentials


def test_a_missing_credential_names_what_is_materialized_and_no_values(monkeypatch):
    module = load()
    listing = {"items": [{
        "metadata": {"name": "doops-cred-1111", "labels": {
            "app.kubernetes.io/managed-by": "doops",
            "doops.sh/credential-id": "cred_present",
        }},
        "data": {},
    }]}
    monkeypatch.setattr(module.subprocess, "run", lambda *a, **k: type(
        "R", (), {"returncode": 0, "stdout": json.dumps(listing).encode(), "stderr": b""}
    )())

    with pytest.raises(module.CredentialError) as failure:
        module.resolve_managed_credential("cred_absent", "harbor-system")

    message = str(failure.value)
    assert "cred_absent" in message
    assert "cred_present" in message, "the error must say what is available"


def test_a_credential_without_the_requested_key_names_only_key_names():
    module = load()
    values = {"CNB_USERNAME": "robot", "CNB_TOKEN": UPSTREAM_TOKEN_CANARY}

    with pytest.raises(module.CredentialError) as failure:
        module.require_key(values, "CNB_PASSWORD", "upstream credential")

    message = str(failure.value)
    assert "CNB_PASSWORD" in message
    assert "CNB_USERNAME" in message
    assert UPSTREAM_TOKEN_CANARY not in message, "a key error must not leak a value"


def test_no_credential_payload_reaches_the_result_or_the_requests(capsys):
    """The payload boundary: nothing printed, returned or sent may carry it."""
    module = load()
    harbor = configured_harbor()

    result = module.run(harbor, args_for(), "robot", UPSTREAM_TOKEN_CANARY)

    printed = capsys.readouterr()
    rendered = json.dumps(result) + printed.out + printed.err
    for canary in (UPSTREAM_TOKEN_CANARY, HARBOR_PASSWORD_CANARY):
        assert canary not in rendered


def test_a_credential_written_to_harbor_is_never_logged(capsys):
    module = load()
    harbor = FakeHarbor(
        registries=[{"id": 2, "name": "cnb-upstream",
                     "url": "https://upstream.example.com",
                     "type": "docker-registry", "insecure": False}],
    )

    module.ensure_registry_endpoint(
        harbor, "cnb-upstream", "https://upstream.example.com",
        "robot", UPSTREAM_TOKEN_CANARY, False,
    )

    printed = capsys.readouterr()
    assert UPSTREAM_TOKEN_CANARY not in printed.out + printed.err
    # The value does reach Harbor's API body, which is the one allowed path.
    assert harbor.puts[0][1]["access_secret"] == UPSTREAM_TOKEN_CANARY


def test_credentials_are_never_passed_as_command_line_arguments():
    """A payload in argv would be visible in the process list and the audit."""
    module = load()
    text = SCRIPT.read_text()

    flags = set()
    for line in text.splitlines():
        if "add_argument(" in line:
            flags.update(re.findall(r'"(--[a-z0-9-]+)"', line))

    # No flag may carry a credential value; the ones that mention a key name it.
    assert "--harbor-credential" in flags
    assert "--upstream-credential" in flags
    for forbidden in ("--harbor-password", "--upstream-token", "--password",
                      "--token", "--secret"):
        assert forbidden not in flags, forbidden + " would put a payload in argv"

    # The credential inputs are managed-credential ids, resolved in-cluster.
    parsed = args_for()
    assert parsed.harbor_credential == "cred_harbor"
    assert parsed.upstream_credential == "cred_upstream"
    assert parsed.harbor_password_key == "HARBOR_ADMIN_PASSWORD"
    assert parsed.upstream_token_key == "CNB_TOKEN"


# -------------------------------------------------------------- script contract


def test_the_script_never_deletes_a_harbor_object():
    """Deleting a policy, a project or a tag is out of scope by contract."""
    text = SCRIPT.read_text()

    assert "def delete" not in text
    assert '"DELETE"' not in text
    assert "client.delete" not in text


def test_plan_is_read_only():
    module = load()
    harbor = configured_harbor()

    rows = module.plan(harbor, args_for(), module.find_registry(harbor, "cnb-upstream"))

    assert harbor.posted == []
    assert harbor.puts == []
    assert [row["action"] for row in rows] == ["reuse"] * 4


def test_plan_reports_what_a_first_run_would_create():
    module = load()
    harbor = FakeHarbor()

    rows = module.plan(harbor, args_for(), None)

    assert [(row["kind"], row["action"]) for row in rows] == [
        ("registry", "create"),
        ("project", "create"),
        ("immutable", "create"),
        ("policy", "create"),
    ]


def test_a_slow_execution_times_out_with_the_right_diagnosis():
    """A copy over a slow link is slow, not failed; the message must say so."""
    module = load()

    class RunningHarbor(FakeHarbor):
        def execution(self, execution_id):
            return {"id": execution_id, "status": "InProgress"}

    with pytest.raises(module.HarborError) as failure:
        module.wait_for_execution(RunningHarbor(), 1918, timeout_seconds=0, poll_seconds=0)

    message = str(failure.value)
    assert "slow, not failed" in message
    assert "/replication/executions/1918/tasks" in message


def test_a_failed_execution_stops_the_run():
    module = load()
    harbor = configured_harbor()

    class FailingHarbor(type(harbor)):
        def execution(self, execution_id):
            return {"id": execution_id, "status": "Failed", "succeed": 0, "failed": 1}

    harbor.__class__ = FailingHarbor

    with pytest.raises(module.HarborError) as failure:
        module.run(harbor, args_with_trigger(), "robot", UPSTREAM_TOKEN_CANARY)

    assert "ended with Failed" in str(failure.value)


def test_harbor_flattens_the_upstream_path_to_its_last_segment():
    module = load()

    assert module.harbor_repository_for("l8ai/agentcloud/backend") == "backend"
    assert module.harbor_repository_for(
        "aiedulab/zhiyong/education/zhiyong-lab-api"
    ) == "zhiyong-lab-api"
