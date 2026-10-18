import importlib.util
import json
import subprocess
from pathlib import Path

import pytest
import yaml


ROOT = Path(__file__).resolve().parents[1]
CHART = ROOT / "deploy" / "helm" / "doops-agent"
SETTINGS_MODULE = ROOT / "agent" / "configure_doagent_settings.py"
KUBECONFIG_MODULE = ROOT / "agent" / "configure_kubeconfig.py"
SANDBOX_ENTRYPOINT = ROOT / "agent" / "sandbox-entrypoint.sh"
IMAGE_DIGEST = "sha256:" + ("b" * 64)


def deployment_values(workload_name, instance, agent_home, selector_labels=None):
    values = {
        "image": {
            "repository": "docker.cnb.cool/l8ai/ai/doops.sh",
            "digest": IMAGE_DIGEST,
            "pullPolicy": "Always",
        },
        "workload": {"name": workload_name},
        "modelRouting": {"policy": "single-model"},
        "imagePullSecrets": [],
        "gateway": {
            "url": "https://doops.l8ai.cn",
            "cluster": "doops-edu",
            "instance": instance,
        },
        "nodeSelector": {"kubernetes.io/hostname": "192.168.0.24"},
        "tolerations": [
            {
                "key": "node-role.kubernetes.io/control-plane",
                "operator": "Exists",
                "effect": "NoSchedule",
            }
        ],
        "hostAliases": [
            {
                "ip": "192.168.0.24",
                "hostnames": ["apiserver.cluster.local"],
            }
        ],
        "doagentSettings": {
            "secretName": "doagent-model-settings",
            "key": "settings.json",
        },
        "volumeMounts": [
            {
                "name": "kube-admin",
                "mountPath": "/etc/kubernetes/admin.conf",
                "readOnly": True,
            },
            {
                "name": "kube-admin",
                "mountPath": "/root/.kube/config",
                "readOnly": True,
            },
            {"name": "agent-home", "mountPath": "/root"},
            {
                "name": "doagent-config",
                "mountPath": "/opt/doagent_config",
                "readOnly": True,
            },
            {
                "name": "containerd-socket",
                "mountPath": "/run/containerd/containerd.sock",
            },
            {
                "name": "nerdctl-bin",
                "mountPath": "/usr/local/bin/nerdctl",
                "readOnly": True,
            },
            {
                "name": "nerdctl-real-bin",
                "mountPath": "/usr/local/bin/nerdctl.real",
                "readOnly": True,
            },
            {
                "name": "crictl-bin",
                "mountPath": "/usr/bin/crictl",
                "readOnly": True,
            },
        ],
        "volumes": [
            {
                "name": "kube-admin",
                "hostPath": {
                    "path": "/etc/kubernetes/admin.conf",
                    "type": "File",
                },
            },
            {
                "name": "agent-home",
                "hostPath": {
                    "path": agent_home,
                    "type": "DirectoryOrCreate",
                },
            },
            {
                "name": "containerd-socket",
                "hostPath": {
                    "path": "/run/containerd/containerd.sock",
                    "type": "Socket",
                },
            },
            {
                "name": "nerdctl-bin",
                "hostPath": {"path": "/usr/bin/nerdctl", "type": "File"},
            },
            {
                "name": "nerdctl-real-bin",
                "hostPath": {
                    "path": "/usr/local/bin/nerdctl.real",
                    "type": "File",
                },
            },
            {
                "name": "crictl-bin",
                "hostPath": {"path": "/usr/bin/crictl", "type": "File"},
            },
        ],
    }
    if selector_labels is not None:
        values["workload"]["selectorLabels"] = selector_labels
    return values


def oilan_values():
    return deployment_values(
        "doops-agent-live",
        "edu-coder",
        "/var/lib/doops-agent-home",
    )


def render_chart(tmp_path, release, values, *overrides):
    values_path = tmp_path / f"{release}-values.yaml"
    values_path.write_text(yaml.safe_dump(values), encoding="utf-8")
    command = [
        "helm",
        "template",
        release,
        str(CHART),
        "--namespace",
        "kz-ops",
        "--values",
        str(values_path),
    ]
    for override in overrides:
        command.extend(["--set-string", override])
    return subprocess.run(command, capture_output=True, text=True)


def render_oilan_chart(tmp_path):
    result = render_chart(
        tmp_path,
        "doops-agent-live",
        oilan_values(),
        f"image.digest={IMAGE_DIGEST}",
    )
    result.check_returncode()
    return [item for item in yaml.safe_load_all(result.stdout) if item][0]


def render_oilan_chart_with(tmp_path, *overrides):
    return render_chart(
        tmp_path,
        "doops-agent-live",
        oilan_values(),
        *overrides,
    )


def env_value(container, name: str):
    return next(item for item in container["env"] if item["name"] == name)


def load_settings_module():
    spec = importlib.util.spec_from_file_location(
        "configure_doagent_settings",
        SETTINGS_MODULE,
    )
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def load_kubeconfig_module():
    spec = importlib.util.spec_from_file_location(
        "configure_kubeconfig",
        KUBECONFIG_MODULE,
    )
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_oilan_agent_model_settings_are_secret_backed(tmp_path):
    deployment = render_oilan_chart(tmp_path)
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    volumes = deployment["spec"]["template"]["spec"]["volumes"]
    settings = next(item for item in volumes if item["name"] == "doagent-config")

    assert deployment["kind"] == "Deployment"
    assert deployment["metadata"]["name"] == "doops-agent-live"
    assert deployment["metadata"]["namespace"] == "kz-ops"
    assert deployment["spec"]["strategy"] == {
        "type": "RollingUpdate",
        "rollingUpdate": {"maxUnavailable": 0, "maxSurge": 1},
    }
    assert deployment["spec"]["template"]["spec"]["hostNetwork"] is False
    assert deployment["spec"]["template"]["spec"]["hostPID"] is False
    assert all("hostPort" not in port for port in container["ports"])
    assert (
        deployment["spec"]["template"]["spec"]["nodeSelector"]["kubernetes.io/hostname"]
        == "192.168.0.24"
    )
    assert deployment["spec"]["template"]["spec"]["tolerations"] == [
        {
            "key": "node-role.kubernetes.io/control-plane",
            "operator": "Exists",
            "effect": "NoSchedule",
        }
    ]
    assert deployment["spec"]["template"]["spec"]["hostAliases"] == [
        {
            "ip": "192.168.0.24",
            "hostnames": ["apiserver.cluster.local"],
        }
    ]
    assert deployment["spec"]["template"]["spec"]["imagePullSecrets"] == []
    assert container["image"] == f"docker.cnb.cool/l8ai/ai/doops.sh@{IMAGE_DIGEST}"
    assert "DOOPS_ALLOW_INSECURE_GATEWAY" not in {
        item["name"] for item in container["env"]
    }
    assert env_value(container, "DOOPS_GATEWAY_URL")["value"] == "https://doops.l8ai.cn"
    assert env_value(container, "DOOPS_GATEWAY_CLUSTER")["value"] == "doops-edu"
    assert env_value(container, "DOOPS_GATEWAY_INSTANCE")["value"] == "edu-coder"
    assert '-listen "0.0.0.0"' in container["command"][-1]
    assert "-agent-token" not in container["command"][-1]
    assert all(
        item["name"] != "DOOPS_AGENT_TOKEN_PATH" for item in container["env"]
    )
    assert all(
        mount["name"] != "agent-registration-token"
        for mount in container["volumeMounts"]
    )
    assert all(
        volume["name"] != "agent-registration-token"
        for volume in deployment["spec"]["template"]["spec"]["volumes"]
    )
    assert "/app/configure_kubeconfig.py" in SANDBOX_ENTRYPOINT.read_text()
    probes = {
        name: container[name]["exec"]["command"][-1]
        for name in ("startupProbe", "readinessProbe", "livenessProbe")
    }
    assert all(
        "pgrep -fc '^/app/doops-agent -listen( |$)'" in probe
        for probe in probes.values()
    )
    assert all("mcp-stdio" not in probe for probe in probes.values())
    assert "/health/live" in probes["startupProbe"]
    assert "/health/ready" in probes["readinessProbe"]
    assert "/health/live" in probes["livenessProbe"]
    assert "configMap" not in settings
    assert settings["secret"] == {
        "secretName": "doagent-model-settings",
        "items": [{"key": "settings.json", "path": "settings.json"}],
    }

    values = oilan_values()
    assert values["modelRouting"] == {"policy": "single-model"}
    assert values["doagentSettings"] == {
        "secretName": "doagent-model-settings",
        "key": "settings.json",
    }

    host_paths = {
        item["name"]: item["hostPath"]["path"]
        for item in volumes
        if "hostPath" in item
    }
    assert host_paths["kube-admin"] == "/etc/kubernetes/admin.conf"
    assert host_paths["agent-home"] == "/var/lib/doops-agent-home"
    assert host_paths["containerd-socket"] == "/run/containerd/containerd.sock"
    assert host_paths["nerdctl-bin"] == "/usr/bin/nerdctl"
    assert host_paths["crictl-bin"] == "/usr/bin/crictl"


def test_runtime_kubeconfig_uses_in_cluster_api_without_changing_admin_identity(
    tmp_path,
):
    module = load_kubeconfig_module()
    source = tmp_path / "admin.conf"
    destination = tmp_path / "runtime.conf"
    source.write_text(
        yaml.safe_dump(
            {
                "apiVersion": "v1",
                "kind": "Config",
                "current-context": "admin@cluster",
                "contexts": [
                    {
                        "name": "admin@cluster",
                        "context": {"cluster": "cluster", "user": "admin"},
                    }
                ],
                "clusters": [
                    {
                        "name": "cluster",
                        "cluster": {
                            "server": "https://apiserver.cluster.local:6443",
                            "certificate-authority-data": "certificate",
                        },
                    }
                ],
                "users": [
                    {
                        "name": "admin",
                        "user": {
                            "client-certificate-data": "client-certificate",
                            "client-key-data": "client-key",
                        },
                    }
                ],
            }
        ),
        encoding="utf-8",
    )

    module.configure_kubeconfig(
        source,
        destination,
        "10.96.0.1",
        "443",
    )

    runtime = yaml.safe_load(destination.read_text(encoding="utf-8"))
    assert runtime["clusters"][0]["cluster"]["server"] == "https://10.96.0.1:443"
    assert runtime["contexts"][0]["context"]["user"] == "admin"
    assert runtime["users"][0]["user"]["client-key-data"] == "client-key"
    assert destination.stat().st_mode & 0o777 == 0o600


def test_oilan_agent_chart_rejects_invalid_image_references(tmp_path):
    invalid_overrides = (
        ("image.digest=latest",),
        (f"image.digest={IMAGE_DIGEST}", "image.repository="),
        ("image.tag=" + ("a" * 129),),
    )

    for overrides in invalid_overrides:
        result = render_oilan_chart_with(tmp_path, *overrides)
        assert result.returncode != 0, result.stdout


def test_public_agent_template_does_not_require_registry_auth():
    values = yaml.safe_load((CHART / "values.yaml").read_text(encoding="utf-8"))

    assert values.get("imagePullSecrets") in (None, [])
    assert all(item.get("name") != "registry-auth" for item in values.get("volumes") or [])


def test_single_model_policy_preserves_mounted_settings_and_omits_duplicate_tiers(
    tmp_path,
):
    module = load_settings_module()
    source = tmp_path / "mounted-settings.json"
    destination = tmp_path / "runtime-settings.json"
    source.write_text(
        json.dumps(
            {
                "model": "quan2go/gpt-5-4",
                "provider": {
                    "quan2go": {
                        "options": {"apiKey": "secret-must-be-preserved"},
                        "models": {"gpt-5-4": {"name": "GPT-5.4"}},
                    }
                },
                "model_tiers": {
                    "high": "quan2go/gpt-5-4",
                    "default": "quan2go/gpt-5-4",
                    "low": "quan2go/gpt-5-4",
                },
            }
        ),
        encoding="utf-8",
    )

    module.configure_settings(source, destination, "single-model")

    rendered = json.loads(destination.read_text(encoding="utf-8"))
    original = json.loads(source.read_text(encoding="utf-8"))
    assert rendered["model"] == "quan2go/gpt-5-4"
    assert (
        rendered["provider"]["quan2go"]["options"]["apiKey"]
        == "secret-must-be-preserved"
    )
    assert "model_tiers" not in rendered
    assert rendered["mcp_servers"] == [module.DOOPS_MCP_SERVER, module.DOSQL_MCP_SERVER]
    assert rendered["permissions"] == module.DOOPS_PERMISSIONS
    assert original["model_tiers"]["high"] == "quan2go/gpt-5-4"


def test_runtime_settings_reject_conflicting_doops_mcp_server(tmp_path):
    module = load_settings_module()
    source = tmp_path / "mounted-settings.json"
    destination = tmp_path / "runtime-settings.json"
    source.write_text(
        json.dumps(
            {
                "model": "quan2go/gpt-5-4",
                "mcp_servers": [
                    {
                        "name": "doops",
                        "command": "/tmp/other-server",
                    }
                ],
            }
        ),
        encoding="utf-8",
    )

    with pytest.raises(module.SettingsConfigurationError, match="runtime-owned"):
        module.configure_settings(source, destination, "single-model")


def test_runtime_settings_reject_conflicting_dosql_mcp_server(tmp_path):
    module = load_settings_module()
    source = tmp_path / "mounted-settings.json"
    destination = tmp_path / "runtime-settings.json"
    source.write_text(
        json.dumps(
            {
                "model": "quan2go/gpt-5-4",
                "mcp_servers": [{"name": "dosql", "command": "/tmp/other-server"}],
            }
        ),
        encoding="utf-8",
    )

    with pytest.raises(module.SettingsConfigurationError, match="runtime-owned"):
        module.configure_settings(source, destination, "single-model")


def test_tiered_policy_rejects_duplicate_model_identifiers(tmp_path):
    module = load_settings_module()
    source = tmp_path / "mounted-settings.json"
    destination = tmp_path / "runtime-settings.json"
    source.write_text(
        json.dumps(
            {
                "model": "quan2go/gpt-5-4",
                "model_tiers": {
                    "high": "quan2go/gpt-5-4",
                    "default": "quan2go/gpt-5-4",
                    "low": "quan2go/gpt-5-4-mini",
                },
            }
        ),
        encoding="utf-8",
    )

    with pytest.raises(module.SettingsConfigurationError, match="distinct"):
        module.configure_settings(source, destination, "tiered")


def agent_container(deployment):
    return next(
        item
        for item in deployment["spec"]["template"]["spec"]["containers"]
        if item["name"] == "doops-agent"
    )


@pytest.mark.parametrize(
    "overrides, want_source, want_account",
    [
        ((), "mounted", None),
        (("kubeconfig.source=serviceAccount", "serviceAccount.name=doops-agent"), "serviceAccount", "doops-agent"),
        (("serviceAccount.name=doops-agent",), "mounted", "doops-agent"),
    ],
)
def test_chart_selects_kubeconfig_source_and_service_account(
    tmp_path, overrides, want_source, want_account
):
    result = render_oilan_chart_with(tmp_path, f"image.digest={IMAGE_DIGEST}", *overrides)
    result.check_returncode()
    deployment = [item for item in yaml.safe_load_all(result.stdout) if item][0]
    pod_spec = deployment["spec"]["template"]["spec"]
    assert pod_spec.get("serviceAccountName") == want_account
    assert env_value(agent_container(deployment), "DOOPS_KUBECONFIG_SOURCE")["value"] == want_source


@pytest.mark.parametrize(
    "override",
    ["kubeconfig.source=inline", "serviceAccount.name=Bad_Name"],
)
def test_chart_rejects_invalid_kubeconfig_source_and_service_account(tmp_path, override):
    result = render_oilan_chart_with(tmp_path, f"image.digest={IMAGE_DIGEST}", override)
    assert result.returncode != 0, result.stdout


def test_entrypoint_generates_service_account_kubeconfig_when_selected():
    text = SANDBOX_ENTRYPOINT.read_text(encoding="utf-8")
    assert 'DOOPS_KUBECONFIG_SOURCE:-mounted}" = "serviceAccount"' in text
    assert "configure_kubeconfig.py --service-account" in text


def test_service_account_kubeconfig_references_rotated_token_without_copying(tmp_path):
    module = load_kubeconfig_module()
    account = tmp_path / "sa"
    account.mkdir()
    (account / "token").write_text("synthetic-sa-token-canary", encoding="utf-8")
    (account / "ca.crt").write_text("synthetic-ca", encoding="utf-8")
    destination = tmp_path / "kube" / "config"

    module.configure_service_account_kubeconfig(destination, "10.96.0.1", "443", account)

    text = destination.read_text(encoding="utf-8")
    assert "synthetic-sa-token-canary" not in text
    config = yaml.safe_load(text)
    assert config["current-context"] == "in-cluster"
    assert config["clusters"][0]["cluster"]["server"] == "https://10.96.0.1:443"
    assert config["clusters"][0]["cluster"]["certificate-authority"] == str(account / "ca.crt")
    assert config["users"][0]["user"] == {"tokenFile": str(account / "token")}
    assert destination.stat().st_mode & 0o777 == 0o600


@pytest.mark.parametrize(
    "files, host, port",
    [
        (("token",), "10.96.0.1", "443"),
        (("ca.crt",), "10.96.0.1", "443"),
        (("token", "ca.crt"), "not-an-ip", "443"),
        (("token", "ca.crt"), "10.96.0.1", "0"),
    ],
)
def test_service_account_kubeconfig_rejects_missing_credentials_and_bad_endpoint(
    tmp_path, files, host, port
):
    module = load_kubeconfig_module()
    account = tmp_path / "sa"
    account.mkdir()
    for name in files:
        (account / name).write_text("synthetic", encoding="utf-8")
    destination = tmp_path / "config"
    with pytest.raises(ValueError):
        module.configure_service_account_kubeconfig(destination, host, port, account)
    assert not destination.exists()
