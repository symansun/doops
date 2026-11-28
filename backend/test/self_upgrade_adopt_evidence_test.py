"""Agent adoption evidence scenarios; all cluster responses are synthetic."""
import base64
import copy
import importlib.util
import json
import hashlib
from pathlib import Path
import subprocess
import sys

import pytest

MODULE = Path(__file__).resolve().parents[1] / "agent" / "adopt_agent.py"
IMAGE = "registry.example/doops@sha256:" + "a" * 64
OLD_IMAGE = "registry.example/doops@sha256:" + "b" * 64
COMMIT = "a" * 40
OLD_COMMIT = "b" * 40
CANARY = "synthetic-sensitive-env-canary"


@pytest.fixture
def module(monkeypatch, tmp_path):
    spec = importlib.util.spec_from_file_location("self_upgrade_adopt_evidence", MODULE)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    for key, value in {
        "DOOPS_UPGRADE_OPERATION_ID": "op-42", "DOOPS_UPGRADE_REQUESTER_ID": "user-42",
        "DOOPS_UPGRADE_AUDIT_ID": "123", "DOOPS_UPGRADE_SESSION_ID": "session-42",
        "DOOPS_UPGRADE_EXPECTED_RELEASE_COMMIT": COMMIT, "DOOPS_UPGRADE_SOURCE_COMMIT": COMMIT,
        "DOOPS_UPGRADE_CI_URL": "https://ci.example/build/42", "DOOPS_UPGRADE_PHASE": "apply",
        "DOOPS_UPGRADE_JOB": "doops-self-upgrade-" + hashlib.sha256(b"op-42").hexdigest()[:24] + "-apply",
        "DOOPS_UPGRADE_RELEASE": "agent", "DOOPS_UPGRADE_NAMESPACE": "test",
        "DOOPS_UPGRADE_WORKLOAD": "agent", "DOOPS_UPGRADE_IMAGE": IMAGE,
        "HOSTNAME": "controller", "TMPDIR": str(tmp_path),
    }.items():
        monkeypatch.setenv(key, value)
    monkeypatch.setattr(module.time, "sleep", lambda _: None)
    return module


def deployment():
    return {
        "apiVersion": "apps/v1", "kind": "Deployment",
        "metadata": {"name": "agent", "namespace": "test", "generation": 1, "uid": "old-deployment"},
        "spec": {"replicas": 1, "selector": {"matchLabels": {"app": "agent"}},
                 "template": {"spec": {"serviceAccountName": "agent", "containers": [{
                     "name": "doops-agent", "image": OLD_IMAGE,
                     "env": [{"name": "DOOPS_GATEWAY_URL", "value": "https://gateway.example"},
                             {"name": "DOOPS_GATEWAY_CLUSTER", "value": "cluster"},
                             {"name": "DOOPS_GATEWAY_INSTANCE", "value": "instance"},
                             {"name": "LEGACY_TOKEN", "value": CANARY}],
                     "volumeMounts": [{"name": "settings", "mountPath": "/opt/doagent_config"}],
                 }], "volumes": [{"name": "settings", "configMap": {"name": "legacy-settings"}}]}}},
        "status": {"observedGeneration": 1, "updatedReplicas": 1, "readyReplicas": 1, "availableReplicas": 1},
    }


class Cluster:
    def __init__(self, module, failure=""):
        self.module, self.failure = module, failure
        self.deployment = deployment()
        self.calls, self.objects = [], []
        self.secret, self.receipt, self.operation_receipt = None, None, None
        self.mutated, self.restored = False, False

    def __call__(self, args, stdin=None, timeout=120):
        self.calls.append(list(args))
        command = " ".join(args)
        if args[:2] == ["helm", "list"]:
            return "[]"
        if args[:2] == ["helm", "history"]:
            return '[{"revision":1,"status":"deployed"}]'
        if args[:2] == ["helm", "upgrade"]:
            if "--dry-run=server" not in args:
                self.mutated = True
                self.deployment["spec"]["template"]["spec"]["containers"][0]["image"] = IMAGE
            return ""
        if "get deployment" in command:
            return json.dumps(self.deployment)
        if "get pods" in command:
            image = IMAGE if self.mutated and not self.restored else OLD_IMAGE
            return json.dumps({"items": [{
                "metadata": {"name": "agent-pod", "uid": "pod-42"},
                "spec": {"nodeName": "node-a", "containers": [{"name": "doops-agent", "image": image}]},
                "status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}],
                           "containerStatuses": [{"name": "doops-agent", "ready": True, "imageID": image}]},
            }]})
        if "get pod controller" in command:
            if self.failure == "controller":
                raise self.module.AdoptError("missing controller")
            return json.dumps({
                "spec": {"nodeName": "node-a", "containers": [{"name": "controller", "image": IMAGE}]},
                "status": {"containerStatuses": [{"name": "controller", "ready": True, "imageID": IMAGE}]},
            })
        if "/app/doops-agent -version" in command:
            commit = COMMIT if self.mutated and not self.restored else OLD_COMMIT
            if self.failure == "commit" and self.mutated and not self.restored:
                commit = "c" * 40
            if self.failure == "baseline" and not self.mutated:
                commit = "unknown"
            return "release=v-test commit=" + commit + "\n"
        if "health/ready" in command:
            if self.failure == "gateway" and self.mutated and not self.restored:
                raise self.module.AdoptError("Gateway is offline")
            return "ready"
        if "get configmap doops-upgrade-verified-" in command:
            return json.dumps(self.receipt) if self.receipt else ""
        if "create -f -" in command:
            if self.failure == "backup":
                raise self.module.AdoptError("Secret write denied")
            self.secret = json.loads(stdin)
            self.objects.append(self.secret)
            return "created"
        if "get secret doops-adopt-backup-" in command:
            return json.dumps(self.secret)
        if "get secrets" in command:
            return '{"items":[{"metadata":{"name":"helm-release"}}]}'
        if "apply -f -" in command:
            obj = json.loads(stdin)
            operation = obj["metadata"]["name"].startswith("doops-upgrade-receipt-")
            if self.failure == "receipt" or self.failure == "operation_receipt" and operation:
                raise self.module.AdoptError("receipt write denied")
            if operation:
                self.operation_receipt = obj
            else:
                self.receipt = obj
            self.objects.append(obj)
            return "applied"
        if "replace --force" in command:
            if self.failure == "restore":
                raise self.module.AdoptError("restore denied")
            self.deployment, self.restored = json.loads(stdin), True
            self.deployment["metadata"]["generation"] = 1
            self.deployment["status"] = deployment()["status"]
            return "restored"
        if "label deployment" in command and self.failure == "restore":
            raise self.module.AdoptError("metadata mutation failed")
        if any(part in command for part in ("label deployment", "annotate deployment", "rollout status", "delete secret")):
            return ""
        raise AssertionError(command)


def execute(module, cluster):
    module.run = cluster
    return module.adopt("test", "agent", "agent", "cluster", "instance", IMAGE)


def test_adoption_keeps_complete_secret_backup_and_writes_attributed_receipt(module):
    cluster = Cluster(module)
    result = execute(module, cluster)
    assert result["outcome"] == "succeeded"
    assert result["release_commit"] == result["expected_release_commit"] == COMMIT
    assert result["upgrade_operation_id"] == "op-42"
    assert result["requester_id"] == "user-42"
    assert result["session_id"] == "session-42" and result["audit_id"] == "123"
    assert result["phase"] == "apply" and result["time"]
    secret = cluster.secret
    assert secret["kind"] == "Secret" and secret["immutable"]
    assert CANARY not in json.dumps(secret)
    stored = json.loads(base64.b64decode(secret["data"]["deployment.json"]))
    assert stored["spec"] == deployment()["spec"]
    assert CANARY in json.dumps(stored)
    assert not any("last-applied" in key for key in secret["metadata"].get("annotations", {}))
    evidence = json.loads(cluster.receipt["data"]["evidence.json"])
    assert evidence["revisions"]["1"]["release_commit"] == COMMIT
    terminal = json.loads(cluster.operation_receipt["data"]["evidence.json"])
    assert terminal["verification"]["release_commit"] == COMMIT
    assert terminal["verification"]["runtimeDigest"] == IMAGE.rsplit("@", 1)[1]
    assert CANARY not in json.dumps(result) + json.dumps(cluster.receipt) + json.dumps(cluster.calls)
    backup_index = next(i for i, call in enumerate(cluster.calls) if "create" in call)
    assert backup_index < next(i for i, call in enumerate(cluster.calls) if "label" in call)


@pytest.mark.parametrize("failure,category,restored", [
    ("backup", "adopt_backup_failed", False),
    ("baseline", "release_commit_unknown", False),
    ("commit", "release_commit_mismatch", True),
    ("gateway", "release_probe_failed", True),
    ("receipt", "verification_receipt_failed", True),
    ("operation_receipt", "verification_receipt_failed", True),
    ("restore", "adopt_restore_failed", False),
    ("controller", "controller_identity_unverified", True),
])
def test_adoption_failure_classification_and_restore(module, failure, category, restored):
    cluster = Cluster(module, failure)
    with pytest.raises(module.AdoptError) as error:
        execute(module, cluster)
    assert error.value.category == category
    assert cluster.restored is restored
    assert CANARY not in str(error.value)
    if restored:
        assert cluster.deployment["spec"] == deployment()["spec"]
        if failure != "operation_receipt":
            assert not cluster.receipt
    if failure in ("backup", "baseline"):
        assert not cluster.mutated
        assert not any("label" in args or "annotate" in args for args in cluster.calls)


def test_unknown_historical_commit_does_not_get_expected_value(module):
    cluster = Cluster(module, "baseline")
    with pytest.raises(module.AdoptError, match="release_commit_unknown"):
        execute(module, cluster)
    assert cluster.secret is None and cluster.receipt is None


def test_receipt_keeps_old_history_and_original_attribution(module):
    cluster = Cluster(module)
    module.run = cluster
    old = {"image": OLD_IMAGE, "release_commit": OLD_COMMIT, "requester_id": "original-user", "outcome": "succeeded"}
    receipt = {"schemaVersion": 1, "release": "agent", "workload": "agent", "revisions": {"0": copy.deepcopy(old)}}
    cluster.receipt = {"data": {"evidence.json": json.dumps(receipt)}}
    current = dict(old, image=IMAGE, release_commit=COMMIT, requester_id="new-user")
    module.write_verified_receipt("test", "agent", "agent", current)
    saved = json.loads(cluster.receipt["data"]["evidence.json"])
    assert saved["revisions"]["0"] == old
    module.write_verified_receipt("test", "agent", "agent", dict(current, requester_id="later-user"))
    saved = json.loads(cluster.receipt["data"]["evidence.json"])
    assert saved["revisions"]["1"]["requester_id"] == "new-user"
    assert saved["last_verification"]["requester_id"] == "later-user"


def test_failed_command_cannot_echo_secret_payload(module):
    with pytest.raises(module.AdoptError) as error:
        module.run([sys.executable, "-c", "import sys; print(" + repr(CANARY) + ", file=sys.stderr); sys.exit(1)"])
    assert CANARY not in str(error.value)


def test_backup_refuses_operation_without_a_safe_identifier(module, monkeypatch):
    monkeypatch.setenv("DOOPS_UPGRADE_OPERATION_ID", "invalid\noperation")
    with pytest.raises(module.AdoptError, match="adopt_backup_failed"):
        module.backup_name("agent")
