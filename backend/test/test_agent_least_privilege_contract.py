"""Render the Agent chart's privilege, identity and capability choices.

These assertions are the contract behind S-02: a non-privileged Agent must fail
to render with host access, keep least privilege when it renders, bind a named
role only where the values say, and the image reference stays documented as a
pin, not silently changed.
"""

import subprocess
from pathlib import Path

import yaml


CHART_ROOT = Path(__file__).resolve().parents[1] / "deploy" / "helm" / "doops-agent"
VALUES = yaml.safe_load((CHART_ROOT / "values.yaml").read_text())


def render(*overrides, namespace="agents-test", check=True):
    command = [
        "helm", "template", "doops-agent-test", str(CHART_ROOT), "-n", namespace,
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
    return [item for item in yaml.safe_load_all(result.stdout) if item]


def container(result):
    return resources(result)[-1]["spec"]["template"]["spec"]["containers"][0]


def bindings(result):
    return [doc for doc in resources(result) if doc["kind"] in {"RoleBinding", "ClusterRoleBinding"}]


def assert_render_fails(*overrides, message):
    result = render(*overrides, check=False)
    assert result.returncode != 0
    assert message in result.stderr, result.stderr


def test_privileged_default_is_true_and_keeps_host_access():
    pod = resources(render())[-1]["spec"]["template"]["spec"]
    assert VALUES["securityContext"]["privileged"] is True
    assert pod["hostPID"] is True
    assert pod["containers"][0]["securityContext"] == {"privileged": True}


def test_unprivileged_agent_drops_every_escalation_path():
    result = render("securityContext.privileged=false", "hostAccess.enabled=false")
    pod = resources(result)[-1]["spec"]["template"]["spec"]
    assert pod["hostPID"] is False
    assert pod["containers"][0]["securityContext"] == {
        "privileged": False,
        "allowPrivilegeEscalation": False,
        "capabilities": {"drop": ["ALL"]},
    }
    assert "hostPath" not in {next(iter(volume)) for volume in pod.get("volumes") or []}


def test_host_access_without_privilege_fails_the_render():
    assert_render_fails(
        "securityContext.privileged=false",
        message="hostAccess.enabled requires securityContext.privileged=true",
    )


def test_cluster_admin_binding_name_is_unchanged():
    overrides = ("rbac.create=true", "kubeconfig.source=serviceAccount", "serviceAccount.name=agent")
    for extra in ((), ("rbac.clusterRole=cluster-admin",)):
        result = render(*overrides, *extra)
        (binding,) = bindings(result)
        assert binding["kind"] == "ClusterRoleBinding"
        assert binding["metadata"]["name"] == "agents-test-agent-cluster-admin"
        assert "namespace" not in binding["metadata"]
        assert binding["roleRef"]["name"] == "cluster-admin"


def test_named_role_uses_a_role_specific_binding_name():
    overrides = (
        "rbac.create=true", "kubeconfig.source=serviceAccount", "serviceAccount.name=agent",
        "rbac.clusterRole=edit",
    )
    (binding,) = bindings(render(*overrides))
    assert binding["kind"] == "ClusterRoleBinding"
    assert binding["metadata"]["name"] == "agents-test-agent-edit"
    assert binding["roleRef"]["name"] == "edit"


def test_namespaces_bind_a_namespaced_role_binding_per_namespace():
    overrides = (
        "rbac.create=true", "kubeconfig.source=serviceAccount", "serviceAccount.name=agent",
        "rbac.clusterRole=edit",
        "rbac.namespaces[0]=team-a", "rbac.namespaces[1]=team-b",
    )
    docs = bindings(render(*overrides))
    assert [(doc["kind"], doc["metadata"]["namespace"]) for doc in docs] == [
        ("RoleBinding", "team-a"), ("RoleBinding", "team-b"),
    ]
    for doc in docs:
        assert doc["metadata"]["name"] == "agents-test-agent-edit"
        assert doc["roleRef"] == {
            "apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "edit",
        }
        assert doc["subjects"] == [{
            "kind": "ServiceAccount", "name": "agent", "namespace": "agents-test",
        }]
    scoped = bindings(render(
        *overrides[:-2], "rbac.clusterRole=view", "rbac.namespaces[0]=team-a",
    ))
    assert [doc["metadata"]["name"] for doc in scoped] == ["agents-test-agent-view"]


def test_rbac_rejects_unknown_role_and_duplicate_namespaces():
    base = ("rbac.create=true", "kubeconfig.source=serviceAccount", "serviceAccount.name=agent")
    for bad in ("rbac.clusterRole=", "rbac.clusterRole=bad role", "rbac.namespaces[0]=Not_A_Namespace",
                "rbac.namespaces="):
        result = render(*base, bad, check=False)
        assert result.returncode != 0, bad
    result = subprocess.run(
        ["helm", "template", "doops-agent-test", str(CHART_ROOT), "-n", "agents-test",
         "--set", "workload.name=doops-agent-test", "--set", "rbac.create=true",
         "--set", "kubeconfig.source=serviceAccount", "--set", "serviceAccount.name=agent",
         "--set-json", 'rbac.namespaces=["team-a","team-a"]'],
        capture_output=True, text=True, check=False,
    )
    assert result.returncode != 0
    assert "/rbac/namespaces" in result.stderr and "are equal" in result.stderr


def test_image_default_is_documented_as_a_compatibility_default():
    # S-02 only documents the pin recommendation; changing the default tag is
    # a separate breaking change and must not ride along silently.
    assert VALUES["image"]["tag"] == "latest"
    text = (CHART_ROOT / "values.yaml").read_text()
    assert "pin image.digest" in text
    pinned = container(render("image.digest=sha256:" + "a" * 64))
    assert pinned["image"] == "registry.example.test/doops-agent@sha256:" + "a" * 64


def test_command_deny_rules_are_documented_as_a_guardrail():
    assert "not a security boundary" in (CHART_ROOT / "values.yaml").read_text()
