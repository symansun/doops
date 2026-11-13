"""Render the identity that Helm will install, including rejected authority changes."""

import subprocess
from pathlib import Path

import pytest
import yaml


CHART = Path(__file__).resolve().parents[1] / "deploy" / "helm" / "doops-agent"


def render(*overrides, namespace="agents-test", check=True):
    command = [
        "helm", "template", "doops-agent-test", str(CHART), "-n", namespace,
        "--set", "workload.name=doops-agent-test",
        "--set", "image.repository=registry.example.test/doops-agent",
        "--set", "gateway.url=https://gateway.example.test",
        "--set", "gateway.cluster=test-cluster",
        "--set", "gateway.instance=test-agent",
        "--set", "gateway.enrollmentTokenSecret=doops-agent-enrollment",
    ]
    for value in overrides:
        flag = "--set-string" if value.startswith("serviceAccount.name=") else "--set"
        command.extend([flag, value])
    return subprocess.run(command, capture_output=True, text=True, check=check)


def resources(result):
    return {item["kind"]: item for item in yaml.safe_load_all(result.stdout) if item}


def test_managed_rbac_binds_exactly_the_pod_identity_and_is_releasable():
    docs = resources(render(
        "rbac.create=true", "kubeconfig.source=serviceAccount",
        "serviceAccount.name=doops-agent-test",
    ))
    assert set(docs) == {"Deployment", "ServiceAccount", "ClusterRoleBinding"}
    account = docs["ServiceAccount"]
    pod = docs["Deployment"]["spec"]["template"]["spec"]
    binding = docs["ClusterRoleBinding"]
    assert account["metadata"]["name"] == pod["serviceAccountName"] == "doops-agent-test"
    assert account["metadata"]["namespace"] == "agents-test"
    assert binding["metadata"]["name"] == "agents-test-doops-agent-test-cluster-admin"
    assert binding["roleRef"] == {
        "apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "cluster-admin",
    }
    assert binding["subjects"] == [{
        "kind": "ServiceAccount", "name": pod["serviceAccountName"], "namespace": "agents-test",
    }]
    for doc in (account, binding):
        assert doc["metadata"]["labels"]["app.kubernetes.io/managed-by"] == "Helm"
        assert "helm.sh/resource-policy" not in doc["metadata"].get("annotations", {})


def test_existing_modes_do_not_gain_an_implicit_cluster_admin_binding():
    assert set(resources(render())) == {"Deployment"}
    docs = resources(render(
        "kubeconfig.source=serviceAccount", "serviceAccount.name=operator-managed-agent",
    ))
    assert set(docs) == {"Deployment"}
    assert docs["Deployment"]["spec"]["template"]["spec"]["serviceAccountName"] == "operator-managed-agent"


@pytest.mark.parametrize("overrides,reason", [
    (["rbac.create=true"], "rbac.create requires kubeconfig.source=serviceAccount"),
    (["kubeconfig.source=serviceAccount"], "serviceAccount.name"),
    (["kubeconfig.source=serviceAccount", "serviceAccount.name=default"], "dedicated ServiceAccount"),
    (["rbac.create=true", "kubeconfig.source=serviceAccount", "serviceAccount.name=default"], "dedicated ServiceAccount"),
    (["rbac.create=maybe"], "boolean"),
])
def test_invalid_identity_combinations_fail_before_install(overrides, reason):
    result = render(*overrides, check=False)
    assert result.returncode != 0
    assert reason in result.stderr
    assert result.stdout == ""


def test_binding_names_keep_namespace_isolation_and_reject_overflow():
    overrides = ["rbac.create=true", "kubeconfig.source=serviceAccount", "serviceAccount.name=doops-agent-test"]
    a = resources(render(*overrides))["ClusterRoleBinding"]
    b = resources(render(*overrides, namespace="other-agents"))["ClusterRoleBinding"]
    assert a["metadata"]["name"] != b["metadata"]["name"]
    assert b["subjects"][0]["namespace"] == "other-agents"
    result = render(
        "rbac.create=true", "kubeconfig.source=serviceAccount",
        "serviceAccount.name=" + "a" * 240, check=False,
    )
    assert result.returncode != 0
    assert "exceed 253 characters" in result.stderr


def test_numeric_service_account_and_namespace_remain_strings():
    docs = resources(render(
        "rbac.create=true", "kubeconfig.source=serviceAccount",
        "serviceAccount.name=123", namespace="123",
    ))
    account = docs["ServiceAccount"]["metadata"]
    assert account["name"] == account["namespace"] == "123"
    assert docs["ClusterRoleBinding"]["subjects"] == [{
        "kind": "ServiceAccount", "name": "123", "namespace": "123",
    }]
