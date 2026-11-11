#!/usr/bin/env python3
"""Inspect an existing Kubernetes doops-agent and legacy recovery snapshots, read-only.

  plan     resolve the image, validate identity and list existing resources.
  run      retired: local mutation cannot satisfy Gateway authorization and audit.
  restore  retired: local mutation cannot satisfy Gateway authorization and audit.

From backend/, on a host with read access through kubectl and helm:
  python3 scripts/reinstall-k8s-agent.py plan --cluster <cluster> --instance <instance>

This is not an initial installer or an upgrade controller. Planning requires an
existing Deployment or legacy backup ConfigMap and never creates a backup, pulls
an image, starts a Pod, installs a release or deletes resources. Local kubeconfig
is used only for inspection; its results are not Gateway audit or release evidence.
Current operation-scoped Secret backups belong to the audited upgrade controller
and are not read by this legacy snapshot inspector.
"""
import argparse
import importlib.util
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

BACKEND = Path(__file__).resolve().parents[1]
DEFAULT_IMAGE = "registry.example.com/doops-agent:latest"
PULLCHECK = "doops-reinstall-pullcheck"
LEFTOVER_PREFIXES = ("doops-adopt-", "doops-self-upgrade-", PULLCHECK)
MANIFEST_TYPES = ", ".join((
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
))
SUPPORTED_PATHS = """For an online Agent with the upgrade controller, use:
  doops -session <session> upgrade --target <target> --mode k8s \\
    --namespace <namespace> --release <release> --image <repository>@sha256:<digest>
This is an image upgrade, not reinstall/restore, and is never invoked by this tool.
Legacy or offline Agent recovery requires a reviewed independent Job submitted via
  doops -session <session> exec --target <online-authorized-management-target>
The Job must back up, verify Ready/digest/Gateway/release_commit, and automatically
restore on verification failure. No generic recovery Job is implemented here.
Without an online authorized Gateway management path, stop recovery. Initial
installation is not supported by this tool; a new identity uses reviewed GitOps
bootstrap. See docs/CLUSTER_INSTALL_GUIDE.md and docs/BUILD_DEPLOY_SOP.md.
"""


def load_adopt():
    spec = importlib.util.spec_from_file_location("adopt_agent", BACKEND / "agent" / "adopt_agent.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


A = load_adopt()
ReinstallError = A.AdoptError


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
    with urlopen(f"{params['realm']}?{urllib.parse.urlencode(query)}", timeout=30) as response:
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
    raw = A.run(["kubectl", "-n", namespace, "get", *args, "--ignore-not-found", "-o", "json"])
    return json.loads(raw) if raw.strip() else None


def source_deployment(namespace, workload):
    """Inspect historical ConfigMap names, independent of new controller backup IDs."""
    for name, key in ((backup_name(workload), "source.json"),
                      (f"doops-adopt-backup-{workload}"[:63], "deployment.json")):
        backup = try_kget(namespace, "configmap", name)
        if backup and key in (backup.get("data") or {}):
            return json.loads(backup["data"][key]), f"configmap/{name}"
    live = try_kget(namespace, "deployment", workload)
    if not live:
        raise ReinstallError(f"no Deployment {workload} and no legacy backup ConfigMap; initial installation is not supported")
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


# --- flow ------------------------------------------------------------------

def prepare(namespace, workload, release, cluster, instance, image):
    """Read and validate the existing recovery source; no installation is attempted."""
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
    found = inventory(namespace, workload, release, source["spec"]["selector"]["matchLabels"])
    return pinned, origin, service_account, found


def plan(namespace, workload, release, cluster, instance, image):
    pinned, origin, service_account, found = prepare(
        namespace, workload, release, cluster, instance, image)
    return {"phase": "plan", "readOnly": True, "mutationSupported": False,
            "image": pinned, "source": origin, "serviceAccount": service_account,
            "release": release, "inventory": found}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, epilog=SUPPORTED_PATHS,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("action", choices=["plan", "run", "restore"],
                        help="plan is read-only; run and restore always fail closed")
    parser.add_argument("--cluster", default="", help="Required for plan: Gateway cluster identity")
    parser.add_argument("--instance", default="", help="Required for plan: Gateway instance identity")
    parser.add_argument("--namespace", default="doops-system")
    parser.add_argument("--workload", default="doops-agent")
    parser.add_argument("--release", default="", help="Helm release name. Default: the workload name")
    parser.add_argument("--image", default=DEFAULT_IMAGE, help="repository:tag or repository@sha256:<digest>")
    parser.add_argument("--kubeconfig", default="", help="Read-only inspection config; never used for mutation")
    parser.add_argument("--backup-dir", help="Retired; no local backups are written")
    args = parser.parse_args(argv)
    if args.action != "plan":
        parser.exit(2, f"Error: local_reinstall_disabled: {args.action} is retired; unaudited local mutation is prohibited.\n"
                       + SUPPORTED_PATHS)
    if args.backup_dir is not None:
        parser.error("--backup-dir is retired; plan never writes backups")
    if not args.cluster.strip() or not args.instance.strip():
        parser.error("plan requires --cluster and --instance")
    if args.kubeconfig:
        os.environ["KUBECONFIG"] = args.kubeconfig
    release = args.release or args.workload
    try:
        result = plan(args.namespace, args.workload, release, args.cluster, args.instance, args.image)
        print(json.dumps(result, indent=2, sort_keys=True))
        return 0
    except ReinstallError as exc:
        print(f"inspection failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
