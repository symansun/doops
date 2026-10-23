#!/usr/bin/env python3
"""Bring an unmanaged doops-agent Deployment under the doops-agent Helm Chart.

  plan     preflight only; prints what run would do.
  run      back up, copy settings to a Secret, mark for Helm, helm install,
           verify the new Pod, and restore the backup on any failure.
  restore  put the backed-up Deployment back and drop the Helm release record.

Settings payloads travel only through kubectl stdin/stdout and are never printed.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import time

CHART = "/app/deploy/helm/doops-agent"
CONTAINER = "doops-agent"
SETTINGS_MOUNT = "/opt/doagent_config"
SETTINGS_KEY = "settings.json"
DROPPED_MOUNTS = {SETTINGS_MOUNT, "/scripts", "/root/.kube", "/root/.kube/config"}
VOLATILE = ("resourceVersion", "uid", "generation", "creationTimestamp", "managedFields", "selfLink")
ADOPTED_LABEL = "doops.sh/adopted-by"
PINNED_IMAGE = re.compile(r"^([a-z0-9.:/_-]+)@(sha256:[0-9a-f]{64})$")


class AdoptError(Exception):
    pass


def run(args, stdin=None, timeout=120):
    result = subprocess.run(args, input=stdin, capture_output=True, text=True, timeout=timeout)
    if result.returncode != 0:
        # stdout may carry Secret data and is never surfaced.
        raise AdoptError(f"{' '.join(args[:4])} failed: {result.stderr.strip()[-400:]}")
    return result.stdout


def kget(namespace, *args):
    return json.loads(run(["kubectl", "-n", namespace, "get", *args, "-o", "json"]))


def backup_name(workload):
    return f"doops-adopt-backup-{workload}"[:63]


def secret_name(workload):
    return f"{workload}-doagent-settings"[:63]


def agent_container(deployment):
    for container in deployment["spec"]["template"]["spec"].get("containers") or []:
        if container.get("name") == CONTAINER:
            return container
    raise AdoptError("deployment has no doops-agent container")


def env_map(container):
    return {e["name"]: e.get("value", "") for e in container.get("env") or [] if "valueFrom" not in e}


def service_account(pod_spec):
    return pod_spec.get("serviceAccountName") or pod_spec.get("serviceAccount") or "default"


def parse_image(image):
    match = PINNED_IMAGE.match(image.strip())
    if not match:
        raise AdoptError("adoption requires a pinned repository@sha256:<digest> image")
    return match.group(1), match.group(2)


def preflight(namespace, workload, release, cluster, instance):
    releases = json.loads(run(["helm", "list", "-n", namespace, "-a", "-o", "json"]) or "[]")
    if any(item.get("name") == release for item in releases):
        raise AdoptError(f"Helm release {release} already exists; use doops upgrade")
    deployment = kget(namespace, "deployment", workload)
    owner = (deployment["metadata"].get("annotations") or {}).get("meta.helm.sh/release-name")
    if owner not in (None, release):
        raise AdoptError(f"deployment is owned by Helm release {owner}")
    return deployment, validate_deployment(deployment, cluster, instance)


def validate_deployment(deployment, cluster, instance):
    """Check that a Deployment can be rendered by the Chart; returns its model settings volume."""
    spec = deployment["spec"]
    if int(spec.get("replicas", 1)) != 1:
        raise AdoptError("adoption requires exactly one replica")
    selector = spec.get("selector") or {}
    if not selector.get("matchLabels") or selector.get("matchExpressions"):
        raise AdoptError("deployment selector must use matchLabels only")
    pod = spec["template"]["spec"]
    if service_account(pod) == "default":
        raise AdoptError("adoption requires a dedicated ServiceAccount")
    container = agent_container(deployment)
    env = env_map(container)
    if env.get("DOOPS_GATEWAY_CLUSTER") != cluster or env.get("DOOPS_GATEWAY_INSTANCE") != instance:
        raise AdoptError("deployment Gateway identity does not match this Agent")
    for key in ("DOOPS_GATEWAY_URL", "DO_AGENT_MODEL_ROUTING_POLICY"):
        if not env.get(key):
            raise AdoptError(f"deployment env {key} is required")
    mount = next((m for m in container.get("volumeMounts") or [] if m.get("mountPath") == SETTINGS_MOUNT), None)
    if not mount:
        raise AdoptError(f"deployment does not mount model settings at {SETTINGS_MOUNT}")
    volume = next((v for v in pod.get("volumes") or [] if v.get("name") == mount["name"]), None)
    if not volume or not ("configMap" in volume or "secret" in volume):
        raise AdoptError("model settings volume must be a ConfigMap or Secret")
    return volume


def settings_source(volume):
    if "configMap" in volume:
        return "configmap", volume["configMap"]["name"]
    return "secret", volume["secret"]["secretName"]


def chart_values(deployment, settings_volume, workload, repository, digest):
    spec = deployment["spec"]
    pod = spec["template"]["spec"]
    container = agent_container(deployment)
    env = env_map(container)
    dropped = {m["name"] for m in container.get("volumeMounts") or [] if m.get("mountPath") in DROPPED_MOUNTS}
    dropped.update({settings_volume["name"], "doagent-config"})
    mounts = [m for m in container.get("volumeMounts") or [] if m["name"] not in dropped]
    kept = {m["name"] for m in mounts}
    # The Chart owns the doagent-config volume but not its mount; without it the Pod exits at start.
    mounts.append({"name": "doagent-config", "mountPath": SETTINGS_MOUNT, "readOnly": True})
    return {
        "image": {"repository": repository, "digest": digest, "pullPolicy": "IfNotPresent"},
        "workload": {"name": workload, "selectorLabels": spec["selector"]["matchLabels"]},
        "modelRouting": {"policy": env["DO_AGENT_MODEL_ROUTING_POLICY"]},
        "doagentSettings": {"secretName": secret_name(workload), "key": SETTINGS_KEY},
        "imagePullSecrets": [{"name": s["name"]} for s in pod.get("imagePullSecrets") or []],
        "serviceAccount": {"name": service_account(pod)},
        "kubeconfig": {"source": "serviceAccount"},
        "gateway": {
            "url": env["DOOPS_GATEWAY_URL"],
            "cluster": env["DOOPS_GATEWAY_CLUSTER"],
            "instance": env["DOOPS_GATEWAY_INSTANCE"],
        },
        "proxy": {"allowedHosts": env.get("DOOPS_PROXY_ALLOW_HOSTS", "")},
        "nodeSelector": pod.get("nodeSelector") or {},
        "tolerations": pod.get("tolerations") or [],
        "hostAliases": pod.get("hostAliases") or [],
        "volumeMounts": mounts,
        "volumes": [v for v in pod.get("volumes") or [] if v.get("name") in kept],
    }


def clean_object(obj):
    metadata = obj.get("metadata") or {}
    for key in VOLATILE:
        metadata.pop(key, None)
    (metadata.get("annotations") or {}).pop("kubectl.kubernetes.io/last-applied-configuration", None)
    obj.pop("status", None)
    return obj


def write_backup(namespace, workload, deployment, settings_kind, settings_name):
    """Back up only non-secret objects; the source settings object is left untouched."""
    name = backup_name(workload)
    backup = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": name, "namespace": namespace, "labels": {ADOPTED_LABEL: "doops-upgrade"}},
        "data": {
            "deployment.json": json.dumps(clean_object(json.loads(json.dumps(deployment))), sort_keys=True),
            "settings-source.json": json.dumps({"kind": settings_kind, "name": settings_name}),
        },
    }
    run(["kubectl", "-n", namespace, "apply", "-f", "-"], stdin=json.dumps(backup))
    return name


def copy_settings_secret(namespace, workload, settings_kind, settings_name):
    source = kget(namespace, settings_kind, settings_name)
    data = source.get("data") or {}
    if SETTINGS_KEY not in data:
        raise AdoptError(f"{settings_kind}/{settings_name} has no {SETTINGS_KEY}")
    secret = {
        "apiVersion": "v1",
        "kind": "Secret",
        "type": "Opaque",
        "metadata": {"name": secret_name(workload), "namespace": namespace,
                     "labels": {ADOPTED_LABEL: "doops-upgrade"}},
    }
    if settings_kind == "configmap":
        secret["stringData"] = {SETTINGS_KEY: data[SETTINGS_KEY]}
    else:
        secret["data"] = {SETTINGS_KEY: data[SETTINGS_KEY]}
    run(["kubectl", "-n", namespace, "apply", "-f", "-"], stdin=json.dumps(secret))
    return secret["metadata"]["name"]


def mark_for_helm(namespace, workload, release):
    run(["kubectl", "-n", namespace, "label", "deployment", workload,
         "app.kubernetes.io/managed-by=Helm", "--overwrite"])
    run(["kubectl", "-n", namespace, "annotate", "deployment", workload,
         f"meta.helm.sh/release-name={release}", f"meta.helm.sh/release-namespace={namespace}", "--overwrite"])


def helm_install(namespace, release, values, chart, dry_run=False):
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
        json.dump(values, fh)
        path = fh.name
    try:
        args = ["helm", "upgrade", "--install", release, chart, "-n", namespace, "-f", path,
                "--timeout", "180s", "--wait=false"]
        if dry_run:
            args.append("--dry-run=server")
        run(args, timeout=240)
    finally:
        os.unlink(path)


def runtime_digest_of(pod):
    for status in (pod.get("status") or {}).get("containerStatuses") or []:
        digest = str(status.get("imageID") or "").split("://")[-1].rsplit("@", 1)[-1]
        if re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            return digest
    return ""


def controller_runtime_digest(namespace, fallback):
    """The adoption Job runs the target image on the cluster runtime, so its own
    imageID is the runtime digest to expect; an OCI index digest can differ."""
    name = os.environ.get("HOSTNAME", "")
    if not name:
        return fallback
    try:
        return runtime_digest_of(kget(namespace, "pod", name)) or fallback
    except AdoptError:
        return fallback


def verify(namespace, workload, image, deadline_seconds=600, interval=5, clock=time.time, sleep=time.sleep,
           runtime_digest=None):
    expected = runtime_digest or parse_image(image)[1]
    deadline = clock() + deadline_seconds
    last = "not checked"
    while clock() < deadline:
        deployment = kget(namespace, "deployment", workload)
        spec, status = deployment["spec"], deployment.get("status") or {}
        if agent_container(deployment).get("image") != image:
            last = "deployment image is not the target"
        elif status.get("observedGeneration") != deployment["metadata"].get("generation"):
            last = "rollout not observed yet"
        elif any(int(status.get(k) or 0) < 1 for k in ("updatedReplicas", "readyReplicas", "availableReplicas")) \
                or int(status.get("unavailableReplicas") or 0) != 0:
            last = "new Pod is not ready"
        else:
            selector = ",".join(f"{k}={v}" for k, v in sorted(spec["selector"]["matchLabels"].items()))
            pods = [p for p in kget(namespace, "pods", "-l", selector).get("items") or []
                    if not p["metadata"].get("deletionTimestamp")]
            ready = [p for p in pods if any(
                c.get("name") == CONTAINER and c.get("ready") and expected in (c.get("imageID") or "")
                for c in (p.get("status") or {}).get("containerStatuses") or [])]
            if ready and len(pods) == len(ready):
                return ready[0]["metadata"]["uid"]
            last = "Pod runtime digest does not match the target"
        sleep(interval)
    raise AdoptError(f"verification failed: {last}")


def restore(namespace, workload, release):
    backup = kget(namespace, "configmap", backup_name(workload))
    deployment = json.loads(backup["data"]["deployment.json"])
    # Drop the Helm record without letting Helm delete the restored workload.
    for secret in kget(namespace, "secrets", "-l", f"owner=helm,name={release}").get("items") or []:
        run(["kubectl", "-n", namespace, "delete", "secret", secret["metadata"]["name"], "--ignore-not-found"])
    run(["kubectl", "-n", namespace, "replace", "--force", "-f", "-"], stdin=json.dumps(deployment), timeout=180)
    run(["kubectl", "-n", namespace, "rollout", "status", f"deployment/{workload}", "--timeout", "300s"], timeout=320)
    return "restored"


def adopt(namespace, workload, release, cluster, instance, image, chart=CHART, plan_only=False, verifier=verify):
    repository, digest = parse_image(image)
    deployment, volume = preflight(namespace, workload, release, cluster, instance)
    values = chart_values(deployment, volume, workload, repository, digest)
    kind, name = settings_source(volume)
    if plan_only:
        return {"phase": "plan", "release": release, "workload": workload, "image": image,
                "serviceAccount": values["serviceAccount"]["name"], "settingsSource": f"{kind}/{name}",
                "secret": secret_name(workload), "backup": backup_name(workload),
                "keptVolumes": [v["name"] for v in values["volumes"]]}
    write_backup(namespace, workload, deployment, kind, name)
    mutated = False
    try:
        copy_settings_secret(namespace, workload, kind, name)
        mark_for_helm(namespace, workload, release)
        mutated = True
        helm_install(namespace, release, values, chart, dry_run=True)
        helm_install(namespace, release, values, chart)
        uid = verifier(namespace, workload, image, runtime_digest=controller_runtime_digest(namespace, digest))
    except Exception as exc:
        if mutated:
            try:
                restore(namespace, workload, release)
            except Exception as restore_exc:
                raise AdoptError(f"{exc}; automatic restore failed: {restore_exc}") from exc
            raise AdoptError(f"{exc}; backup restored") from exc
        raise
    return {"phase": "adopted", "release": release, "workload": workload, "image": image, "podUID": uid}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("action", choices=["plan", "run", "restore"])
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--workload", required=True)
    parser.add_argument("--release", required=True)
    parser.add_argument("--cluster", default=os.environ.get("DOOPS_GATEWAY_CLUSTER", ""))
    parser.add_argument("--instance", default=os.environ.get("DOOPS_GATEWAY_INSTANCE", ""))
    parser.add_argument("--image", default="")
    parser.add_argument("--chart", default=CHART)
    args = parser.parse_args(argv)
    try:
        if args.action == "restore":
            print(json.dumps({"phase": restore(args.namespace, args.workload, args.release)}))
            return 0
        if not args.cluster or not args.instance:
            raise AdoptError("--cluster and --instance are required")
        result = adopt(args.namespace, args.workload, args.release, args.cluster, args.instance,
                       args.image, chart=args.chart, plan_only=args.action == "plan")
        print(json.dumps(result, sort_keys=True))
        return 0
    except AdoptError as exc:
        print(f"adopt failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
