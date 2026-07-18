import os
import subprocess
from pathlib import Path

import pytest
import yaml


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "backend" / "scripts" / "bootstrap-release-runner.sh"


def test_bootstrap_release_runner_preserves_managed_values(tmp_path):
    fake_bin = tmp_path / "bin"
    fake_bin.mkdir()
    captured_values = tmp_path / "runner-values.yaml"
    captured_args = tmp_path / "helm-upgrade-args.txt"
    helm = fake_bin / "helm"
    helm.write_text(
        """#!/bin/sh
set -eu
if [ "$1" = "get" ] && [ "$2" = "values" ]; then
  cat <<'YAML'
image:
  repository: old.example/doops
  digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  pullPolicy: Always
workload:
  name: doops-agent
  selectorLabels:
    app: doops-agent
modelRouting:
  policy: single-model
doagentSettings:
  secretName: doagent-model-settings
  key: settings.json
gateway:
  url: https://doops.l8ai.cn
  cluster: doops-235
  instance: hdu-235
nodeSelector:
  kubernetes.io/hostname: node-1
volumeMounts:
  - name: agent-home
    mountPath: /root
  - name: kubeconfig
    mountPath: /root/.kube/config
volumes:
  - name: kubeconfig
    hostPath:
      path: /etc/rancher/k3s/k3s.yaml
      type: File
  - name: agent-home
    hostPath:
      path: /var/lib/doops-agent-home
      type: DirectoryOrCreate
YAML
  exit 0
fi
if [ "$1" = "upgrade" ]; then
  printf '%s\\n' "$@" >"${CAPTURED_ARGS}"
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--values" ]; then
      cp "$2" "${CAPTURED_VALUES}"
      exit 0
    fi
    shift
  done
fi
exit 1
""",
        encoding="utf-8",
    )
    helm.chmod(0o755)

    env = os.environ.copy()
    env["PATH"] = f"{fake_bin}:{env['PATH']}"
    env["CAPTURED_VALUES"] = str(captured_values)
    env["CAPTURED_ARGS"] = str(captured_args)
    image = (
        "docker.cnb.cool/l8ai/ai/doops.sh@sha256:"
        "df0038d05fd8a895f3d9cf73ad7ce5b286bd19f4e7c091292a90be64de366fb5"
    )
    subprocess.run(
        [
            "bash",
            str(SCRIPT),
            "--namespace",
            "ai",
            "--managed-release",
            "doops-agent",
            "--runner-release",
            "doops-release-runner",
            "--runner-workload",
            "doops-release-runner",
            "--runner-instance",
            "release-runner-hdu",
            "--runner-home",
            "/var/lib/doops-release-runner-home",
            "--image",
            image,
        ],
        cwd=ROOT / "backend",
        env=env,
        check=True,
        text=True,
        capture_output=True,
    )

    values = yaml.safe_load(captured_values.read_text(encoding="utf-8"))
    assert values["workload"] == {
        "name": "doops-release-runner",
        "selectorLabels": {"app": "doops-release-runner"},
    }
    assert values["gateway"] == {
        "url": "https://doops.l8ai.cn",
        "cluster": "doops-235",
        "instance": "release-runner-hdu",
    }
    assert values["image"] == {
        "repository": "docker.cnb.cool/l8ai/ai/doops.sh",
        "digest": "sha256:df0038d05fd8a895f3d9cf73ad7ce5b286bd19f4e7c091292a90be64de366fb5",
        "pullPolicy": "Always",
    }
    assert values["doagentSettings"]["secretName"] == "doagent-model-settings"
    assert values["nodeSelector"] == {"kubernetes.io/hostname": "node-1"}
    assert values["volumeMounts"][1]["name"] == "kubeconfig"
    assert values["volumes"][0]["name"] == "kubeconfig"
    assert values["volumes"][1]["hostPath"] == {
        "path": "/var/lib/doops-release-runner-home",
        "type": "DirectoryOrCreate",
    }

    args = captured_args.read_text(encoding="utf-8").splitlines()
    assert args[:3] == [
        "upgrade",
        "doops-release-runner",
        str(ROOT / "backend" / "deploy" / "helm" / "doops-agent"),
    ]
    assert "--install" in args
    assert "--atomic" in args
    assert "--namespace" in args
    assert "ai" in args


def test_bootstrap_release_runner_rejects_mutable_image():
    result = subprocess.run(
        [
            "bash",
            str(SCRIPT),
            "--namespace",
            "ai",
            "--managed-release",
            "doops-agent",
            "--runner-release",
            "doops-release-runner",
            "--runner-workload",
            "doops-release-runner",
            "--runner-instance",
            "release-runner-hdu",
            "--runner-home",
            "/var/lib/doops-release-runner-home",
            "--image",
            "docker.cnb.cool/l8ai/ai/doops.sh:v1.2.21",
        ],
        cwd=ROOT / "backend",
        text=True,
        capture_output=True,
    )

    assert result.returncode == 2
    assert "immutable repository@sha256 digest" in result.stderr


def test_bootstrap_release_runner_rejects_managed_release_name():
    result = subprocess.run(
        [
            "bash",
            str(SCRIPT),
            "--namespace",
            "ai",
            "--managed-release",
            "doops-agent",
            "--runner-release",
            "doops-agent",
            "--runner-workload",
            "doops-release-runner",
            "--runner-instance",
            "release-runner-hdu",
            "--runner-home",
            "/var/lib/doops-release-runner-home",
            "--image",
            (
                "docker.cnb.cool/l8ai/ai/doops.sh@sha256:"
                "df0038d05fd8a895f3d9cf73ad7ce5b286bd19f4e7c091292a90be64de366fb5"
            ),
        ],
        cwd=ROOT / "backend",
        text=True,
        capture_output=True,
    )

    assert result.returncode == 2
    assert "must differ from the managed release" in result.stderr


@pytest.mark.parametrize(
    ("runner_workload", "runner_instance", "runner_home", "error"),
    [
        (
            "doops-agent",
            "release-runner-hdu",
            "/var/lib/doops-release-runner-home",
            "runner workload must differ from the managed workload",
        ),
        (
            "doops-release-runner",
            "hdu-235",
            "/var/lib/doops-release-runner-home",
            "runner instance must differ from the managed instance",
        ),
        (
            "doops-release-runner",
            "release-runner-hdu",
            "/var/lib/doops-agent-home",
            "runner home must differ from the managed agent home",
        ),
    ],
)
def test_bootstrap_release_runner_requires_distinct_identity(
    tmp_path, runner_workload, runner_instance, runner_home, error
):
    fake_bin = tmp_path / "bin"
    fake_bin.mkdir()
    helm = fake_bin / "helm"
    helm.write_text(
        """#!/bin/sh
if [ "$1" = "get" ] && [ "$2" = "values" ]; then
  cat <<'YAML'
image: {repository: old.example/doops, digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}
workload: {name: doops-agent, selectorLabels: {app: doops-agent}}
gateway: {url: https://doops.l8ai.cn, cluster: doops-235, instance: hdu-235}
volumes:
  - name: agent-home
    hostPath: {path: /var/lib/doops-agent-home, type: DirectoryOrCreate}
YAML
  exit 0
fi
exit 99
""",
        encoding="utf-8",
    )
    helm.chmod(0o755)
    env = os.environ.copy()
    env["PATH"] = f"{fake_bin}:{env['PATH']}"

    result = subprocess.run(
        [
            "bash",
            str(SCRIPT),
            "--namespace",
            "ai",
            "--managed-release",
            "doops-agent",
            "--runner-release",
            "doops-release-runner",
            "--runner-workload",
            runner_workload,
            "--runner-instance",
            runner_instance,
            "--runner-home",
            runner_home,
            "--image",
            (
                "docker.cnb.cool/l8ai/ai/doops.sh@sha256:"
                "df0038d05fd8a895f3d9cf73ad7ce5b286bd19f4e7c091292a90be64de366fb5"
            ),
        ],
        cwd=ROOT / "backend",
        env=env,
        text=True,
        capture_output=True,
    )

    assert result.returncode != 0
    assert error in result.stderr


def test_bootstrap_release_runner_requires_isolated_agent_home(tmp_path):
    fake_bin = tmp_path / "bin"
    fake_bin.mkdir()
    helm = fake_bin / "helm"
    helm.write_text(
        """#!/bin/sh
if [ "$1" = "get" ] && [ "$2" = "values" ]; then
  cat <<'YAML'
image: {repository: old.example/doops, digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}
workload: {name: doops-agent, selectorLabels: {app: doops-agent}}
gateway: {url: https://doops.l8ai.cn, cluster: doops-235, instance: hdu-235}
volumes: []
YAML
  exit 0
fi
exit 99
""",
        encoding="utf-8",
    )
    helm.chmod(0o755)
    env = os.environ.copy()
    env["PATH"] = f"{fake_bin}:{env['PATH']}"

    result = subprocess.run(
        [
            "bash",
            str(SCRIPT),
            "--namespace",
            "ai",
            "--managed-release",
            "doops-agent",
            "--runner-release",
            "doops-release-runner",
            "--runner-workload",
            "doops-release-runner",
            "--runner-instance",
            "release-runner-hdu",
            "--runner-home",
            "/var/lib/doops-release-runner-home",
            "--image",
            (
                "docker.cnb.cool/l8ai/ai/doops.sh@sha256:"
                "df0038d05fd8a895f3d9cf73ad7ce5b286bd19f4e7c091292a90be64de366fb5"
            ),
        ],
        cwd=ROOT / "backend",
        env=env,
        text=True,
        capture_output=True,
    )

    assert result.returncode != 0
    assert "does not define an agent-home hostPath volume" in result.stderr
