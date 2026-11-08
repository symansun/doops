#!/usr/bin/env python3
"""Clean out a broken Kubernetes doops-agent and reinstall it from the Helm Chart.

  plan     read-only: resolve the image and list what run would back up and delete.
  run      pull-check the image on the cluster, back up, delete the old workload,
           helm install the Chart, verify the new Pod, and restore the backup on failure.
  restore  drop the Helm release and re-apply the backed-up Deployment.

Run from backend/ on a host whose kubectl and helm reach the target cluster:

  python3 scripts/reinstall-k8s-agent.py plan --cluster doops-jm --instance jm-228
  python3 scripts/reinstall-k8s-agent.py run  --cluster doops-jm --instance jm-228

Nothing is deleted before the image has been pulled and started on the cluster.
Model settings stay inside the cluster; payloads are never printed or written to disk.
"""
import argparse
import importlib.util
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

BACKEND = Path(__file__).resolve().parents[1]
CHART = BACKEND / "deploy" / "helm" / "doops-agent"
DEFAULT_IMAGE = "ghcr.io/symansun/doops-agent:latest"
PULLCHECK = "doops-reinstall-pullcheck"
LEFTOVER_PREFIXES = ("doops-adopt-", "doops-self-upgrade-", PULLCHECK)
MANIFEST_TYPES = ", ".join((
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
))


def load_adopt():
    spec = importlib.util.spec_from_file_location("adopt_agent", BACKEND / "agent" / "adopt_agent.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


A = load_adopt()
ReinstallError = A.AdoptError


def log(message):
    print(f"==> {message}", file=sys.stderr, flush=True)


def backup_name(workload):
    return f"doops-reinstall-backup-{workload}"[:63]


# --- image -----------------------------------------------------------------

def split_reference(image):
    name, _, tag = image.rpartition(":")
    if not name or "/" in tag:
        name, tag = image, "latest"
    host, _, path = name.partition("/")
    if not path or "." not in host:
        raise ReinstallError(f"image must name its registry host: {image}")
    return name, host, path, tag


def bearer_token(challenge, path, urlopen):
    params = dict(re.findall(r'(\w+)="([^"]*)"', challenge))
    if not challenge.lower().startswith("bearer") or "realm" not in params:
        raise ReinstallError("registry did not offer a bearer token challenge")
    query = {"scope": f"repository:{path}:pull"}
    if "service" in params:
        query["service"] = params["service"]
    realm = params["realm"]
    if not realm.startswith(("http://", "https://")):
        realm = "https://" + realm
    with urlopen(f"{realm}?{urllib.parse.urlencode(query)}", timeout=30) as response:
        body = json.load(response)
    token = body.get("token") or body.get("access_token")
    if not token:
        raise ReinstallError("registry token response had no token")
    return token


def resolve_image(image, urlopen=urllib.request.urlopen):
    """Pin a tag to the registry's manifest digest; pinned references pass through."""
    if "@" in image:
        A.parse_image(image)
        return image
    name, host, path, tag = split_reference(image)
    url = f"https://{host}/v2/{path}/manifests/{tag}"
    headers = {"Accept": MANIFEST_TYPES}
    for _attempt in range(2):
        request = urllib.request.Request(url, method="HEAD", headers=headers)
        try:
            with urlopen(request, timeout=30) as response:
                digest = response.headers.get("Docker-Content-Digest", "")
                break
        except urllib.error.HTTPError as exc:
            if exc.code != 401 or "Authorization" in headers:
                raise ReinstallError(f"registry returned {exc.code} for {image}") from exc
            headers["Authorization"] = "Bearer " + bearer_token(exc.headers.get("WWW-Authenticate", ""), path, urlopen)
    else:
        raise ReinstallError(f"could not resolve {image}")
    pinned = f"{name}@{digest}"
    A.parse_image(pinned)
    return pinned


# --- cluster reads ---------------------------------------------------------

def try_kget(namespace, *args):
    try:
        return A.kget(namespace, *args)
    except ReinstallError as exc:
        if "NotFound" in str(exc) or "not found" in str(exc):
            return None
        raise


def source_deployment(namespace, workload):
    """The last known-good Agent spec: a previous reinstall or adoption backup, else live."""
    for name, key in ((backup_name(workload), "source.json"), (A.backup_name(workload), "deployment.json")):
        backup = try_kget(namespace, "configmap", name)
        if backup and key in (backup.get("data") or {}):
            return json.loads(backup["data"][key]), f"configmap/{name}"
    live = try_kget(namespace, "deployment", workload)
    if not live:
        raise ReinstallError(f"no Deployment {workload} and no backup ConfigMap to rebuild it from")
    return A.clean_object(live), f"deployment/{workload}"


def helm_release_exists(namespace, release):
    releases = json.loads(A.run(["helm", "list", "-n", namespace, "-a", "-o", "json"]) or "[]")
    return any(item.get("name") == release for item in releases)


def owned(item, selector):
    metadata = item.get("metadata") or {}
    labels = metadata.get("labels") or {}
    if selector and all(labels.get(k) == v for k, v in selector.items()):
        return True
    return str(metadata.get("name", "")).startswith(LEFTOVER_PREFIXES)


def inventory(namespace, workload, release, selector):
    found = {"helmRelease": release if helm_release_exists(namespace, release) else "",
             "deployment": workload if try_kget(namespace, "deployment", workload) else ""}
    for kind in ("replicasets", "jobs", "pods"):
        items = (A.kget(namespace, kind).get("items") or [])
        found[kind] = [i["metadata"]["name"] for i in items if owned(i, selector)]
    found["stuckPods"] = [i["metadata"]["name"] for i in A.kget(namespace, "pods").get("items") or []
                          if owned(i, selector) and i["metadata"].get("deletionTimestamp")]
    return found


# --- pull check ------------------------------------------------------------

def pullcheck_manifest(namespace, image, pod_spec):
    spec = {
        "restartPolicy": "Never",
        "automountServiceAccountToken": False,
        "containers": [{"name": "pullcheck", "image": image, "imagePullPolicy": "IfNotPresent",
                        "command": ["/app/doops-agent", "-version"]}],
    }
    for key in ("nodeSelector", "tolerations", "imagePullSecrets", "runtimeClassName"):
        if pod_spec.get(key):
            spec[key] = pod_spec[key]
    return {"apiVersion": "v1", "kind": "Pod",
            "metadata": {"name": PULLCHECK, "namespace": namespace, "labels": {A.ADOPTED_LABEL: "reinstall"}},
            "spec": spec}


def waiting_reason(pod):
    for status in (pod.get("status") or {}).get("containerStatuses") or []:
        reason = ((status.get("state") or {}).get("waiting") or {}).get("reason", "")
        if reason:
            return reason
    return ""


def pull_check(namespace, image, pod_spec, deadline_seconds=300, clock=time.time, sleep=time.sleep):
    """Start the image once on the cluster; returns (runtime digest, release line)."""
    A.run(["kubectl", "-n", namespace, "delete", "pod", PULLCHECK, "--ignore-not-found", "--wait=true"])
    A.run(["kubectl", "-n", namespace, "apply", "-f", "-"],
          stdin=json.dumps(pullcheck_manifest(namespace, image, pod_spec)))
    try:
        deadline, pull_failures, last = clock() + deadline_seconds, 0, "pending"
        while clock() < deadline:
            pod = A.kget(namespace, "pod", PULLCHECK)
            phase = (pod.get("status") or {}).get("phase", "")
            if phase == "Succeeded":
                line = A.run(["kubectl", "-n", namespace, "logs", PULLCHECK]).strip().splitlines()[-1:]
                return A.runtime_digest_of(pod), (line[0] if line else "")
            if phase == "Failed":
                raise ReinstallError("pull check container exited non-zero")
            last = waiting_reason(pod) or phase
            pull_failures = pull_failures + 1 if last in ("ErrImagePull", "ImagePullBackOff") else 0
            if pull_failures >= 6:
                break
            sleep(5)
        events = A.run(["kubectl", "-n", namespace, "get", "events",
                        "--field-selector", f"involvedObject.name={PULLCHECK}"])
        raise ReinstallError(f"image cannot start on the cluster ({last}):\n{events.strip()[-1500:]}")
    finally:
        A.run(["kubectl", "-n", namespace, "delete", "pod", PULLCHECK, "--ignore-not-found", "--wait=false"])


# --- backup / delete / restore ----------------------------------------------

def write_backup(namespace, workload, release, source, found, backup_dir):
    data = {"source.json": json.dumps(source, sort_keys=True), "inventory.json": json.dumps(found, sort_keys=True)}
    live = try_kget(namespace, "deployment", workload)
    if live:
        data["live.json"] = json.dumps(A.clean_object(live), sort_keys=True)
    if found["helmRelease"]:
        data["helm-values.json"] = A.run(["helm", "get", "values", release, "-n", namespace, "-o", "json"])
    backup = {"apiVersion": "v1", "kind": "ConfigMap",
              "metadata": {"name": backup_name(workload), "namespace": namespace,
                           "labels": {A.ADOPTED_LABEL: "reinstall"}},
              "data": data}
    A.run(["kubectl", "-n", namespace, "apply", "-f", "-"], stdin=json.dumps(backup))
    if backup_dir:
        path = Path(backup_dir)
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
        for key, value in data.items():
            (path / key).write_text(value)
            (path / key).chmod(0o600)
    return backup["metadata"]["name"]


def delete_old(namespace, workload, found, clock=time.time, sleep=time.sleep):
    if found["helmRelease"]:
        A.run(["helm", "uninstall", found["helmRelease"], "-n", namespace], timeout=240)
    A.run(["kubectl", "-n", namespace, "delete", "deployment", workload, "--ignore-not-found",
           "--wait=true", "--timeout=120s"], timeout=150)
    for kind in ("jobs", "replicasets", "pods"):
        if found[kind]:
            A.run(["kubectl", "-n", namespace, "delete", kind, *found[kind], "--ignore-not-found", "--wait=false"])
    deadline = clock() + 60
    remaining = found["pods"]
    while remaining and clock() < deadline:
        live = {i["metadata"]["name"] for i in A.kget(namespace, "pods").get("items") or []}
        remaining = [name for name in remaining if name in live]
        if remaining:
            sleep(5)
    if remaining:
        # Stuck in Terminating (usually a lost node): only the API record is removed.
        log(f"force-removing stuck pods: {', '.join(remaining)}")
        A.run(["kubectl", "-n", namespace, "delete", "pods", *remaining,
               "--grace-period=0", "--force", "--ignore-not-found"])


def restore(namespace, workload, release):
    backup = A.kget(namespace, "configmap", backup_name(workload))
    source = json.loads(backup["data"]["source.json"])
    if helm_release_exists(namespace, release):
        A.run(["helm", "uninstall", release, "-n", namespace], timeout=240)
    A.run(["kubectl", "-n", namespace, "delete", "deployment", workload, "--ignore-not-found", "--wait=true"],
          timeout=180)
    A.run(["kubectl", "-n", namespace, "apply", "-f", "-"], stdin=json.dumps(source))
    return "restored"


# --- flow ------------------------------------------------------------------

def prepare(namespace, workload, release, cluster, instance, image):
    """Every check that can refuse runs here, before the cluster is touched."""
    pinned = resolve_image(image)
    live = try_kget(namespace, "deployment", workload)
    owner = (((live or {}).get("metadata") or {}).get("annotations") or {}).get("meta.helm.sh/release-name")
    if owner not in (None, release):
        raise ReinstallError(f"deployment {workload} belongs to Helm release {owner}; pass --release {owner}")
    source, origin = source_deployment(namespace, workload)
    A.validate_deployment(source, cluster, instance)
    service_account = A.service_account(source["spec"]["template"]["spec"])
    if try_kget(namespace, "serviceaccount", service_account) is None:
        raise ReinstallError(f"serviceaccount/{service_account} must exist; the new Agent runs as it")
    # The reinstalled Agent receives its model provider from the Gateway.
    repository, digest = A.parse_image(pinned)
    values = A.chart_values(source, workload, repository, digest)
    found = inventory(namespace, workload, release, source["spec"]["selector"]["matchLabels"])
    return pinned, source, origin, values, found


def reinstall(namespace, workload, release, cluster, instance, image, chart=CHART, plan_only=False,
              backup_dir="", verifier=None, checker=pull_check, deleter=delete_old):
    pinned, source, origin, values, found = prepare(
        namespace, workload, release, cluster, instance, image)
    summary = {"image": pinned, "source": origin,
               "serviceAccount": values["serviceAccount"]["name"], "release": release, "delete": found}
    if plan_only:
        return {"phase": "plan", **summary}
    log(f"pull-checking {pinned} on the cluster")
    runtime_digest, release_line = checker(namespace, pinned, source["spec"]["template"]["spec"])
    log(f"candidate {release_line or '<no release line>'}")
    write_backup(namespace, workload, release, source, found, backup_dir)
    log(f"backed up to configmap/{backup_name(workload)}" + (f" and {backup_dir}" if backup_dir else ""))
    try:
        deleter(namespace, workload, found)
        A.helm_install(namespace, release, values, str(chart), dry_run=True)
        A.helm_install(namespace, release, values, str(chart))
        log("waiting for the new Pod to become Ready (Ready means the Gateway tunnel is up)")
        uid = (verifier or A.verify)(namespace, workload, pinned, runtime_digest=runtime_digest or None)
    except Exception as exc:
        try:
            restore(namespace, workload, release)
        except Exception as restore_exc:
            raise ReinstallError(f"{exc}; automatic restore failed: {restore_exc}") from exc
        raise ReinstallError(f"{exc}; backup restored") from exc
    return {"phase": "installed", **summary, "podUID": uid, "candidate": release_line}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("action", choices=["plan", "run", "restore"])
    parser.add_argument("--cluster", required=True, help="Gateway cluster the Agent registers as")
    parser.add_argument("--instance", required=True, help="Gateway instance the Agent registers as")
    parser.add_argument("--namespace", default="doops-system")
    parser.add_argument("--workload", default="doops-agent")
    parser.add_argument("--release", default="", help="Helm release name. Default: the workload name")
    parser.add_argument("--image", default=DEFAULT_IMAGE, help="repository:tag or repository@sha256:<digest>")
    parser.add_argument("--kubeconfig", default="")
    parser.add_argument("--backup-dir", default="", help="Also write the backup here. Default: ~/doops-agent-backup-<time>")
    args = parser.parse_args(argv)
    if args.kubeconfig:
        os.environ["KUBECONFIG"] = args.kubeconfig
    release = args.release or args.workload
    try:
        if args.action == "restore":
            print(json.dumps({"phase": restore(args.namespace, args.workload, release)}))
            return 0
        backup_dir = args.backup_dir or str(Path.home() / f"doops-agent-backup-{time.strftime('%Y%m%d-%H%M%S')}")
        result = reinstall(args.namespace, args.workload, release, args.cluster, args.instance, args.image,
                           plan_only=args.action == "plan", backup_dir=backup_dir)
        print(json.dumps(result, indent=2, sort_keys=True))
        if result["phase"] == "installed":
            print(f"Next, from the operator machine: doops -session reinstall targets --target <target for "
                  f"{args.cluster}/{args.instance}> and compare RELEASE_COMMIT with the candidate line above.")
        return 0
    except ReinstallError as exc:
        print(f"reinstall failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
