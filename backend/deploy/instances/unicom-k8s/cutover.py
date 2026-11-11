"""One-time 0.2.42 cutover; run only in the pinned, independent release Job."""
import json
import hashlib
import os
from pathlib import Path
import signal
import subprocess
import time
import yaml


NAMESPACE = "doops-system"
OLD_RELEASE = "doops-unicom"
NEW_RELEASE = "doops-agent"
RECEIPT = "doagent-unicom-042-cutover"
IMAGE = "registry.example.com/doops-agent@sha256:fd20c510411d9dde8e2061ed390e3b91877781ade2d4400a39b715280524b02a"
COMMIT = "8683d67174e19a1750e9b19de99a0816cdb8ea79"
KUBECTL = ["kubectl", "--request-timeout=20s", "-n", NAMESPACE]


def run(*args, data=None, timeout=60):
    result = subprocess.run(
        args, input=data, text=True, capture_output=True, timeout=timeout
    )
    if result.returncode:
        raise RuntimeError(f"{args[0]} {args[1]} failed: {result.stderr[-2000:]}")
    return result.stdout


def get(kind, name):
    return json.loads(run(*KUBECTL, "get", kind, name, "-o", "json"))


def receipt(**fields):
    run(*KUBECTL, "patch", "configmap", RECEIPT, "--type=merge",
        "-p", json.dumps({"data": fields}))
    print(json.dumps(fields), flush=True)


def ready_candidate():
    deployment = get("deployment", NEW_RELEASE)
    assert deployment["status"].get("observedGeneration") == deployment["metadata"]["generation"]
    assert deployment["status"].get("readyReplicas") == 1
    pods = json.loads(run(*KUBECTL, "get", "pods", "-l", "app=doops-agent", "-o", "json"))
    assert len(pods["items"]) == 1, "candidate Pod is not unique"
    pod = pods["items"][0]
    assert not pod["metadata"].get("deletionTimestamp")
    status = pod["status"]["containerStatuses"]
    assert len(status) == 1 and status[0]["ready"]
    assert status[0]["imageID"] == IMAGE
    assert pod["spec"]["containers"][0]["image"] == IMAGE
    return pod["metadata"]["name"], pod["metadata"]["uid"]


def stop(signum, frame):
    raise RuntimeError(f"controller interrupted by signal {signum}")


def main():
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    run("python3", "/app/configure_kubeconfig.py", "--service-account",
        os.environ["KUBECONFIG"], os.environ["KUBERNETES_SERVICE_HOST"],
        os.environ["KUBERNETES_SERVICE_PORT"])
    assert run("/app/doops-agent", "-version").strip() == f"release=v1.3.123 commit={COMMIT}"
    assert run("/usr/local/bin/do-agent", "--version").strip() == "do-agent 0.2.42"

    releases = json.loads(run("helm", "list", "-a", "-n", NAMESPACE, "-o", "json"))
    assert [r["name"] for r in releases if r["chart"].startswith("doops-agent-")] == [OLD_RELEASE]
    history = run("helm", "history", OLD_RELEASE, "-n", NAMESPACE, "-o", "json")
    assert [r["revision"] for r in json.loads(history) if r["status"] == "deployed"] == [5]
    values = run("helm", "get", "values", OLD_RELEASE, "-n", NAMESPACE, "-o", "json")
    assert json.loads(values)["gateway"] == {
        "cluster": "doops-unicom", "instance": "unicom-k8s", "url": "https://doops.l8ai.cn"
    }
    manifest = run("helm", "get", "manifest", OLD_RELEASE, "-n", NAMESPACE)
    objects = [x for x in yaml.safe_load_all(manifest) if x]
    assert [(x["kind"], x["metadata"]["name"]) for x in objects] == [("Deployment", OLD_RELEASE)]
    old = get("deployment", OLD_RELEASE)
    assert old["status"].get("readyReplicas") == 1
    old_uid = old["metadata"]["uid"]
    chart = yaml.safe_load(run("helm", "show", "chart", "/app/deploy/helm/doops-agent"))
    assert (chart["name"], chart["version"]) == ("doops-agent", "0.3.0")
    asset_dir = Path(__file__).parent
    asset_values = (asset_dir / "values.yaml").read_bytes()
    candidate_values = yaml.safe_load(asset_values)
    assert candidate_values["gateway"] == json.loads(values)["gateway"]
    assert candidate_values["image"]["repository"] + "@" + candidate_values["image"]["digest"] == IMAGE
    backup = {
        "apiVersion": "v1", "kind": "ConfigMap",
        "metadata": {"name": RECEIPT, "namespace": NAMESPACE},
        "data": {
            "phase": "preflight", "oldUID": old_uid, "oldHistory": history,
            "oldValues": values, "oldManifest": manifest,
            "oldDeployment": json.dumps(old), "image": IMAGE, "commit": COMMIT,
            "chartVersion": chart["version"],
            "valuesSHA256": hashlib.sha256(asset_values).hexdigest(),
            "controllerSHA256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        },
    }
    run(*KUBECTL, "create", "-f", "-", data=json.dumps(backup))
    helm = ["helm", "upgrade", "--install", NEW_RELEASE, "/app/deploy/helm/doops-agent",
            "-n", NAMESPACE, "--reset-values", "-f", "/release/values.yaml"]
    new_created = False
    retiring_old = False
    try:
        run(*helm, "--dry-run=server", timeout=90)
        receipt(phase="installing")
        new_created = True
        run(*helm, "--atomic", "--wait", "--timeout", "180s", timeout=210)
        pod_name, pod_uid = ready_candidate()
        assert get("deployment", OLD_RELEASE)["metadata"]["uid"] == old_uid
        receipt(phase="candidate-ready", podName=pod_name, podUID=pod_uid)
        deadline = time.monotonic() + 600
        while time.monotonic() < deadline:
            data = get("configmap", RECEIPT)["data"]
            if data.get("verifiedPodUID") == pod_uid:
                assert data.get("acceptanceSession")
                assert data.get("functionMarker") == "RELEASE_SMOKE_UNICOM_042"
                break
            time.sleep(5)
        else:
            raise RuntimeError("Gateway/function acceptance was not received within 600 seconds")
        assert ready_candidate() == (pod_name, pod_uid)
        assert get("deployment", OLD_RELEASE)["metadata"]["uid"] == old_uid
        assert run("helm", "get", "manifest", OLD_RELEASE, "-n", NAMESPACE) == manifest
        receipt(phase="retiring-old")
        retiring_old = True
        run("helm", "uninstall", OLD_RELEASE, "-n", NAMESPACE, "--wait",
            "--timeout", "120s", timeout=150)
        remaining = json.loads(run(*KUBECTL, "get", "deployments", "-o", "json"))
        assert OLD_RELEASE not in [x["metadata"]["name"] for x in remaining["items"]]
        assert ready_candidate() == (pod_name, pod_uid)
        receipt(phase="complete")
    except BaseException as error:
        print(json.dumps({"phase": "failed", "cause": str(error)}), flush=True)
        try:
            if retiring_old:
                # The accepted candidate is now the only recoverable service.
                receipt(phase="old-cleanup-incomplete", error=str(error))
            else:
                if new_created:
                    releases = json.loads(run("helm", "list", "-a", "-n", NAMESPACE, "-o", "json"))
                    if any(r["name"] == NEW_RELEASE for r in releases):
                        run("helm", "uninstall", NEW_RELEASE, "-n", NAMESPACE,
                            "--wait", "--timeout", "120s", timeout=150)
                    run(*KUBECTL, "rollout", "status", f"deployment/{OLD_RELEASE}",
                        "--timeout=120s", timeout=150)
                assert get("deployment", OLD_RELEASE)["metadata"]["uid"] == old_uid
                receipt(phase="old-preserved", error=str(error))
        except BaseException as recovery_error:
            print(json.dumps({"phase": "recovery-failed", "cause": str(error),
                              "recovery": str(recovery_error)}), flush=True)
            raise RuntimeError(f"cutover failed: {error}; recovery failed: {recovery_error}") from recovery_error
        raise


if __name__ == "__main__":
    main()
