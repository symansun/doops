package server

// Embedded shell scripts for the in-cluster Agent upgrade controller.
//
// These constants are the contract with the cluster: they are executed inside
// the upgrade Job, so they are kept verbatim and in one file to make review and
// diffing practical. The Go side that composes and interprets them lives in
// self_upgrade.go.
const selfUpgradeEvidencePrelude = `set -eu
umask 077
failure_category=upgrade_preflight_failed
emit_upgrade_event() {
  python3 - "$1" "$2" <<'PY'
import json, os, sys
sys.path.insert(0, "/app")
from adopt_agent import evidence_context, evidence_exception_hook, write_operation_receipt
sys.excepthook = evidence_exception_hook
record = evidence_context(os.environ["DOOPS_UPGRADE_NAMESPACE"], os.environ["DOOPS_UPGRADE_WORKLOAD"],
                          os.environ["DOOPS_UPGRADE_RELEASE"], os.environ.get("DOOPS_UPGRADE_IMAGE", ""))
record.update(outcome=sys.argv[1], category=sys.argv[2])
if sys.argv[1] in ("succeeded", "failed"):
    record = write_operation_receipt(sys.argv[1], sys.argv[2])
print(json.dumps(record, sort_keys=True))
PY
}
finish_upgrade() {
  status="$1"
  trap - EXIT
  if [ -s "${TMPDIR:-/tmp}/doops-upgrade-terminal.json" ]; then exit "$status"; fi
  if [ "$status" -eq 0 ]; then
    emit_upgrade_event succeeded "" || exit 1
  else
    category_file="${TMPDIR:-/tmp}/doops-upgrade-category"
    if [ -s "$category_file" ]; then failure_category="$(cat "$category_file")"; fi
    echo "category=${failure_category}" >&2
    emit_upgrade_event failed "$failure_category" || exit 1
  fi
  exit "$status"
}
trap 'finish_upgrade $?' EXIT
emit_upgrade_event running ""
`

const selfUpgradeKubeconfigPrelude = selfUpgradeEvidencePrelude + `failure_category=kubeconfig_unavailable
sleep 5
source=/root/.kube/config
export KUBECONFIG=/tmp/doops-kubeconfig
if [ "${DOOPS_UPGRADE_KUBECONFIG_SOURCE:-mounted}" = "serviceAccount" ]; then
  python3 /app/configure_kubeconfig.py --service-account "$KUBECONFIG" "$KUBERNETES_SERVICE_HOST" "${KUBERNETES_SERVICE_PORT_HTTPS:-443}"
elif [ ! -f "$source" ]; then
  echo "kubeconfig volume is missing" >&2
  exit 1
elif [ -n "${KUBERNETES_SERVICE_HOST:-}" ]; then
  python3 /app/configure_kubeconfig.py "$source" "$KUBECONFIG" "$KUBERNETES_SERVICE_HOST" "${KUBERNETES_SERVICE_PORT_HTTPS:-443}"
else
  cp "$source" "$KUBECONFIG"
  chmod 600 "$KUBECONFIG"
fi
ns="$DOOPS_UPGRADE_NAMESPACE"
release="$DOOPS_UPGRADE_RELEASE"
workload="$DOOPS_UPGRADE_WORKLOAD"
failure_category=upgrade_preflight_failed
`

const selfUpgradeDeploymentVerifier = `verify_deployment() {
  python3 - "$ns" "$workload" "$1" "${2:-controller}" "${3:-}" <<'PY'
import json
import os
import re
import subprocess
import sys
sys.path.insert(0, "/app")
from adopt_agent import evidence_context, evidence_exception_hook, verified_release_commit, write_verified_receipt
sys.excepthook = evidence_exception_hook

namespace, expected_workload, expected_image, identity_source, record = sys.argv[1:6]
deployment = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "deployment", expected_workload, "-o", "json"],
    timeout=30,
))

metadata = deployment.get("metadata") or {}
spec = deployment.get("spec") or {}
status = deployment.get("status") or {}
name = str(metadata.get("name") or "")
if name != expected_workload:
    raise SystemExit(
        f"Deployment identity changed: {name or '<unknown>'} != {expected_workload}"
    )

generation = int(metadata.get("generation") or 0)
observed_generation = int(status.get("observedGeneration") or 0)
if generation <= 0 or observed_generation != generation:
    raise SystemExit("Deployment controller has not observed the current generation")

desired = int(spec.get("replicas") if spec.get("replicas") is not None else 1)
if desired <= 0:
    raise SystemExit(f"Deployment desiredReplicas={desired} must be positive")
for field in ("updatedReplicas", "readyReplicas", "availableReplicas"):
    actual = int(status.get(field) or 0)
    if actual < desired:
        raise SystemExit(
            f"Deployment {field}={actual}, desiredReplicas={desired}"
        )
if int(status.get("replicas") or 0) < desired:
    raise SystemExit(
        f"Deployment replicas={int(status.get('replicas') or 0)}, desiredReplicas={desired}"
    )
if int(status.get("unavailableReplicas") or 0) != 0:
    raise SystemExit("Deployment still has unavailable replicas")

containers = ((spec.get("template") or {}).get("spec") or {}).get("containers") or []
actual_image = next(
    (container.get("image") for container in containers
     if container.get("name") == "doops-agent"),
    None,
)
if actual_image != expected_image:
    raise SystemExit(
        f"Deployment doops-agent image mismatch: {actual_image or '<none>'} != {expected_image}"
    )

selector = ((spec.get("selector") or {}).get("matchLabels") or {})
if not selector:
    raise SystemExit("Deployment selector.matchLabels is empty")
selector_text = ",".join(f"{key}={value}" for key, value in sorted(selector.items()))
pods = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "pods", "-l", selector_text, "-o", "json"],
    timeout=30,
)).get("items") or []
pods = [pod for pod in pods if not (pod.get("metadata") or {}).get("deletionTimestamp")]
matching_pods = []
for pod in pods:
    containers = (pod.get("spec") or {}).get("containers") or []
    container = next((item for item in containers if item.get("name") == "doops-agent"), None)
    if not container or container.get("image") != expected_image:
        ready = any(
            condition.get("type") == "Ready" and condition.get("status") == "True"
            for condition in (pod.get("status") or {}).get("conditions") or []
        )
        if ready:
            raise SystemExit("Active Ready Agent Pod still uses a different image")
        continue
    matching_pods.append(pod)
if len(matching_pods) < desired:
    raise SystemExit(f"Active Agent Pods={len(matching_pods)}, desiredReplicas={desired}")
pods = matching_pods
# A digest-pinned reference names its content, so a Pod on any node can be
# checked against it. A tag only names what one node's runtime pulled.
pinned_digest = expected_image.rsplit("@", 1)[1] if "@sha256:" in expected_image else ""
if not re.fullmatch(r"sha256:[0-9a-f]{64}", pinned_digest):
    pinned_digest = ""
expected_digest = pinned_digest
controller_node = ""
runtime_class = ""
expected_commit = os.environ.get("DOOPS_UPGRADE_EXPECTED_RELEASE_COMMIT", "")
if identity_source == "baseline":
    baseline = json.loads(os.environ["DOOPS_UPGRADE_BASELINE"])
    if expected_image != baseline.get("image"):
        raise SystemExit("Rollback image does not match the captured baseline")
    expected_digest = baseline.get("digest") or ""
    controller_node = baseline.get("node") or ""
    runtime_class = baseline.get("runtime_class") or ""
    expected_commit = baseline.get("release_commit") or ""
    if not controller_node or not re.fullmatch(r"sha256:[0-9a-f]{64}", expected_digest):
        raise SystemExit("Rollback baseline runtime identity is missing")
elif identity_source == "controller":
    if expected_image != os.environ.get("DOOPS_UPGRADE_IMAGE"):
        raise SystemExit("Controller image does not match the requested image")
    controller = json.loads(subprocess.check_output(
        ["kubectl", "-n", namespace, "get", "pod", os.environ["HOSTNAME"], "-o", "json"],
        timeout=30,
    ))
    controller_spec = controller.get("spec") or {}
    controller_node = controller_spec.get("nodeName") or ""
    runtime_class = controller_spec.get("runtimeClassName") or ""
    pinned = next((item for item in controller_spec.get("containers") or []
                   if item.get("name") == "controller"), None)
    running = next((item for item in (controller.get("status") or {}).get("containerStatuses") or []
                    if item.get("name") == "controller"), None)
    if not controller_node or not pinned or pinned.get("image") != expected_image:
        raise SystemExit("Controller Pod does not match the requested image and node")
    if not running or not running.get("ready"):
        raise SystemExit("Controller container is not Ready")
    expected_digest = str(running.get("imageID") or "").split("://")[-1].rsplit("@", 1)[-1]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", expected_digest):
        raise SystemExit("Controller Pod imageID has no valid sha256 digest")
else:
    raise SystemExit("Unknown runtime identity source")
verified_pods = []
for pod in pods:
    metadata = pod.get("metadata") or {}
    uid = str(metadata.get("uid") or "")
    if not uid:
        raise SystemExit("Agent Pod is missing metadata.uid")
    status = pod.get("status") or {}
    if status.get("phase") != "Running" or not any(
        condition.get("type") == "Ready" and condition.get("status") == "True"
        for condition in status.get("conditions") or []
    ):
        raise SystemExit("Agent Pod is not Running and Ready")
    containers = (pod.get("spec") or {}).get("containers") or []
    container = next((item for item in containers if item.get("name") == "doops-agent"), None)
    if not container or container.get("image") != expected_image:
        raise SystemExit("Agent Pod spec image does not match the expected image")
    pod_node = (pod.get("spec") or {}).get("nodeName") or ""
    if not pod_node:
        raise SystemExit("Agent Pod is not bound to a node")
    same_node = pod_node == controller_node
    if not same_node and not pinned_digest:
        raise SystemExit("Agent and Controller Pods must use the same node runtime")
    if ((pod.get("spec") or {}).get("runtimeClassName") or "") != runtime_class:
        raise SystemExit("Agent and Controller Pods must use the same runtime class")
    statuses = status.get("containerStatuses") or []
    agent = next((item for item in statuses if item.get("name") == "doops-agent"), None)
    if not agent or not agent.get("ready"):
        raise SystemExit("Agent container is not Ready")
    image_id = str(agent.get("imageID") or "")
    actual_digest = image_id.split("://")[-1].rsplit("@", 1)[-1]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", actual_digest):
        raise SystemExit("Agent Pod imageID has no valid sha256 digest")
    if same_node:
        if expected_digest and actual_digest != expected_digest:
            raise SystemExit("Agent Pod imageID does not match the expected digest")
    elif actual_digest not in (pinned_digest, expected_digest):
        # Another node pulled the pinned reference itself. Its runtime may report
        # the pinned digest or the platform digest the controller saw; any other
        # digest is different content.
        raise SystemExit(
            "Agent Pod on node " + pod_node + " reports a digest that matches neither the pinned image"
            " nor the controller runtime on node " + controller_node
        )
    actual_commit = verified_release_commit(namespace, metadata["name"], expected_commit)
    verified_pods.append({"podUID": uid, "runtimeDigest": actual_digest, "node": pod_node,
                          "runtimeClass": runtime_class, "release_commit": actual_commit})
    print("pod_verified uid=" + uid + " imageID=" + image_id + " node=" + pod_node)
print("deployment_verified=" + name + " generation=" + str(generation) + " release_commit=" + actual_commit)
if record == "record":
    release = os.environ["DOOPS_UPGRADE_RELEASE"]
    evidence = evidence_context(namespace, expected_workload, release, expected_image)
    evidence.update(verified_pods[0], pods=verified_pods, outcome="succeeded", category="",
                    verification_stage=os.environ.get("DOOPS_UPGRADE_VERIFICATION_STAGE", identity_source),
                    expected_release_commit=expected_commit)
    write_verified_receipt(namespace, release, expected_workload, evidence)
PY
}
`

const selfUpgradeRevisionImage = `revision_image() {
  python3 - "$release" "$ns" "$1" "$workload" <<'PY'
import subprocess, sys, yaml
release, namespace, revision, workload = sys.argv[1:5]
manifest = subprocess.check_output(
    ["helm", "get", "manifest", release, "-n", namespace, "--revision", revision],
    timeout=30,
)
images = [
    container["image"]
    for doc in yaml.safe_load_all(manifest) if doc and doc.get("kind") == "Deployment"
    and (doc.get("metadata") or {}).get("name") == workload
    and (doc.get("metadata") or {}).get("namespace", namespace) == namespace
    for container in ((doc.get("spec") or {}).get("template") or {}).get("spec", {}).get("containers") or []
    if container.get("name") == "doops-agent"
]
if len(images) != 1 or not images[0]:
    raise SystemExit("rollback_baseline_image_unresolved: the revision must contain exactly one doops-agent image")
print(images[0])
PY
}
`

const selfUpgradeRollbackBaselineProbe = `rollback_baseline_probe="$(python3 - "$ns" "$rollback_baseline_image" "$previous" "$baseline" <<'PY' 2>&1
import base64, json, os, subprocess, sys, tempfile

namespace, image, revision, baseline_json = sys.argv[1:5]
baseline = json.loads(baseline_json)
CATEGORY_UNAVAILABLE = "rollback_baseline_image_unavailable"
CATEGORY_PROBE_FAILED = "rollback_baseline_probe_failed"
# A refused credential is checked first: some registries answer a refused
# request for a repository with "not found", and that is not a missing image.
REFUSED_MARKERS = ("unauthorized", "denied", "forbidden", "authentication required")
MISSING_MARKERS = ("manifest unknown", "name unknown", "not found")


def fail(category, reason):
    raise SystemExit(category + ": image=" + image + " reason=" + reason)


def pull_secret_auths(name):
    read = subprocess.run(["kubectl", "-n", namespace, "get", "secret", name, "-o", "json"],
                          capture_output=True, timeout=30)
    if read.returncode != 0:
        fail(CATEGORY_PROBE_FAILED, "the pull secret " + name + " could not be read")
    data = (json.loads(read.stdout).get("data") or {})
    try:
        if data.get(".dockerconfigjson"):
            return json.loads(base64.b64decode(data[".dockerconfigjson"])).get("auths") or {}
        if data.get(".dockercfg"):
            return json.loads(base64.b64decode(data[".dockercfg"]))
    except (ValueError, TypeError, AttributeError):
        pass
    fail(CATEGORY_PROBE_FAILED, "the pull secret " + name + " is not a registry credential")


auths = {}
for ref in baseline.get("image_pull_secrets") or []:
    for server, entry in pull_secret_auths(ref.get("name") or "").items():
        auths.setdefault(server, entry)

command = ["skopeo", "inspect", "--raw", "--retry-times", "2"]
authfile = ""
try:
    if auths:
        fd, authfile = tempfile.mkstemp(prefix="doops-rollback-baseline-", suffix=".json")
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            json.dump({"auths": auths}, fh)
        command += ["--authfile", authfile]
    else:
        command.append("--no-creds")
    try:
        checked = subprocess.run(command + ["docker://" + image], capture_output=True, timeout=120)
    except subprocess.TimeoutExpired:
        fail(CATEGORY_PROBE_FAILED, "the registry did not answer in time")
    except OSError:
        fail(CATEGORY_PROBE_FAILED, "skopeo is not available in the upgrade image")
finally:
    if authfile:
        os.unlink(authfile)

if checked.returncode == 0:
    print("rollback_baseline_image_available image=" + image)
    raise SystemExit(0)
detail = checked.stderr.decode("utf-8", "replace").strip()
lowered = detail.lower()
if any(marker in lowered for marker in REFUSED_MARKERS):
    fail(CATEGORY_PROBE_FAILED, "the registry refused the pull credentials")
if any(marker in lowered for marker in MISSING_MARKERS):
    fail(CATEGORY_UNAVAILABLE, "the registry has no manifest for this image")
fail(CATEGORY_PROBE_FAILED, "the registry manifest check failed")
PY
)" || {
  case "$rollback_baseline_probe" in
    *rollback_baseline_image_unavailable*)
      if [ "${DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE:-0}" != "1" ]; then
        failure_category=rollback_baseline_image_unavailable
        echo "$rollback_baseline_probe" >&2
        exit 1
      fi
      echo "warning: $rollback_baseline_probe; the operator allowed this upgrade without a pullable rollback baseline" >&2
      ;;
    *)
      echo "rollback_baseline_probe_failed: $rollback_baseline_probe" >&2
      exit 1
      ;;
  esac
}
`

const selfUpgradeApplyChecks = selfUpgradeRevisionImage + selfUpgradeRBACGuard + `repository="$DOOPS_UPGRADE_REPOSITORY"
failure_category=rollback_baseline_image_unresolved
tag="${DOOPS_UPGRADE_TAG:-}"
digest="${DOOPS_UPGRADE_DIGEST:-}"
target="$DOOPS_UPGRADE_IMAGE"
chart=` + selfUpgradeChartPath + `
live_deployment="${TMPDIR:-/tmp}/doops-upgrade-live-deployment.json"
previous_image="$(python3 - "$ns" "$workload" "$live_deployment" <<'PY'
import json, subprocess, sys
ns, workload, live_path = sys.argv[1:4]
raw = subprocess.check_output(["kubectl", "-n", ns, "get", "deployment", workload, "-o", "json"])
with open(live_path, "wb") as fh:
    fh.write(raw)
doc = json.loads(raw)
containers = ((doc.get("spec") or {}).get("template") or {}).get("spec", {}).get("containers") or []
actual_image = next(
    (container.get("image") for container in containers
     if container.get("name") == "doops-agent"),
    None,
)
if not actual_image:
    raise SystemExit("deployment must contain a doops-agent container")
print(actual_image)
PY
)"
previous="$(helm history "$release" -n "$ns" -o json | python3 -c 'import json,sys; rows=json.load(sys.stdin); deployed=[int(row["revision"]) for row in rows if row["status"]=="deployed"]; print(max(deployed) if len(deployed)==1 else "")')"
test -n "$previous" || { echo "rollback_baseline_image_unresolved: the release must have exactly one deployed revision" >&2; exit 1; }
selector_json="$(kubectl -n "$ns" get deployment "$workload" -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(json.dumps(d["spec"]["selector"]["matchLabels"], separators=(",", ":"), sort_keys=True))')"
baseline="$(python3 - "$ns" "$previous_image" "$selector_json" <<'PY'
import json, re, subprocess, sys
sys.path.insert(0, "/app")
from adopt_agent import evidence_exception_hook, verified_release_commit
sys.excepthook = evidence_exception_hook
ns, image, labels = sys.argv[1:4]
selector = ",".join(f"{key}={value}" for key, value in sorted(json.loads(labels).items()))
pods = json.loads(subprocess.check_output(
    ["kubectl", "-n", ns, "get", "pods", "-l", selector, "-o", "json"], timeout=30,
)).get("items") or []
pods = [pod for pod in pods if not (pod.get("metadata") or {}).get("deletionTimestamp")]
if len(pods) != 1:
    raise SystemExit("Rollback baseline requires one active Agent Pod")
pod = pods[0]
spec = pod.get("spec") or {}
container = next((item for item in spec.get("containers") or [] if item.get("name") == "doops-agent"), None)
status = next((item for item in (pod.get("status") or {}).get("containerStatuses") or [] if item.get("name") == "doops-agent"), None)
if not (pod.get("metadata") or {}).get("uid") or not spec.get("nodeName") or not container or container.get("image") != image or not status or not status.get("ready"):
    raise SystemExit("Rollback baseline Pod identity is not verified")
digest = str(status.get("imageID") or "").split("://")[-1].rsplit("@", 1)[-1]
if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
    raise SystemExit("Rollback baseline runtime digest is invalid")
commit = verified_release_commit(ns, pod["metadata"]["name"])
print(json.dumps({"image": image, "digest": digest, "node": spec["nodeName"],
                 "release_commit": commit,
                 "runtime_class": spec.get("runtimeClassName") or "",
                 "service_account": spec.get("serviceAccountName") or "",
                 "tolerations": spec.get("tolerations") or [],
                 "node_selector": spec.get("nodeSelector") or {},
                 "image_pull_secrets": spec.get("imagePullSecrets") or []}))
PY
)"
export DOOPS_UPGRADE_BASELINE="$baseline"
failure_category=rollback_baseline_verification_failed
verify_deployment "$previous_image" baseline ${verify_record-record}
failure_category=rollback_baseline_image_unresolved
rollback_baseline_image="$(revision_image "$previous")"
test "$rollback_baseline_image" = "$previous_image" || {
  echo "rollback_baseline_image_unresolved: the rollback revision image differs from the live Agent image" >&2
  exit 1
}
failure_category=rollback_baseline_probe_failed
` + selfUpgradeRollbackBaselineProbe + `failure_category=upgrade_values_failed
values="${TMPDIR:-/tmp}/doops-upgrade-values.json"
helm get values "$release" -n "$ns" -o json >"$values"
python3 - "$values" "$repository" "$tag" "$digest" "$selector_json" <<'PY'
import json, sys
path, repository, tag, digest, selector_json = sys.argv[1:6]
with open(path, encoding="utf-8") as fh:
    values = json.load(fh)
image = values.setdefault("image", {})
image.pop("tag", None)
image["repository"] = repository
image.pop("digest", None)
if digest:
    image["digest"] = digest
else:
    image["tag"] = tag
values.setdefault("workload", {})["selectorLabels"] = json.loads(selector_json)
with open(path, "w", encoding="utf-8") as fh:
    json.dump(values, fh)
PY
failure_category=stale_volume_mounts_unresolved
` + selfUpgradeStaleVolumeMounts + `failure_category=rbac_controller_identity_changed
verify_controller_rbac "$previous_manifest" "$rendered_manifest"
`

const selfUpgradeRBACGuard = `verify_controller_rbac() {
  python3 - "$ns" "$1" "$2" <<'PY'
import json, os, subprocess, sys, yaml

namespace, before_path, after_path = sys.argv[1:4]


def refuse(reason):
    raise SystemExit("rbac_controller_identity_changed: " + reason + "; use the authorized external controller")


def identities(path, account_namespace, account):
    result = {}
    with open(path, encoding="utf-8") as fh:
        for doc in yaml.safe_load_all(fh):
            if not doc:
                continue
            kind = doc.get("kind")
            metadata = doc.get("metadata") or {}
            name = metadata.get("name")
            resource_namespace = "" if kind == "ClusterRoleBinding" else metadata.get("namespace", namespace)
            identity = None
            if kind == "ServiceAccount" and name == account and resource_namespace == account_namespace:
                identity = {}
            elif kind in ("RoleBinding", "ClusterRoleBinding"):
                subjects = doc.get("subjects") or []
                if any(subject.get("kind") == "ServiceAccount" and subject.get("name") == account
                       and subject.get("namespace", resource_namespace) == account_namespace for subject in subjects):
                    identity = {"roleRef": doc.get("roleRef"),
                                "subjects": sorted(json.dumps(subject, sort_keys=True) for subject in subjects)}
            if identity is not None:
                key = (kind, resource_namespace, name)
                if key in result:
                    refuse("duplicate controller RBAC resource in the Helm manifest")
                result[key] = identity
    return result


try:
    authentication = json.loads(subprocess.check_output(
        ["kubectl", "auth", "whoami", "-o", "json"], timeout=30,
    ))
    username = ((authentication.get("status") or {}).get("userInfo") or {}).get("username")
    if not isinstance(username, str) or not username:
        refuse("Kubernetes did not return the controller authentication identity")
    source_is_account = os.environ.get("DOOPS_UPGRADE_KUBECONFIG_SOURCE") == "serviceAccount"
    if not username.startswith("system:serviceaccount:"):
        if source_is_account:
            refuse("the controller authentication does not match its ServiceAccount source")
        print("controller_rbac=independent")
        raise SystemExit(0)
    identity = username.split(":")
    if len(identity) != 4 or not identity[2] or not identity[3]:
        refuse("Kubernetes returned an invalid ServiceAccount identity")
    account_namespace, account = identity[2:4]
    if source_is_account:
        pod = json.loads(subprocess.check_output(
            ["kubectl", "-n", namespace, "get", "pod", os.environ["HOSTNAME"], "-o", "json"], timeout=30,
        ))
        if account_namespace != namespace or (pod.get("spec") or {}).get("serviceAccountName") != account:
            refuse("the controller Pod ServiceAccount does not match its authentication identity")
    if identities(before_path, account_namespace, account) != identities(after_path, account_namespace, account):
        refuse("Helm would change the controller ServiceAccount or its RBAC bindings")
except (OSError, subprocess.SubprocessError, ValueError, yaml.YAMLError) as exc:
    refuse("cannot verify controller RBAC: " + str(exc))
print("controller_rbac=unchanged")
PY
}
`

const selfUpgradeStaleVolumeMounts = `rendered_manifest="${TMPDIR:-/tmp}/doops-upgrade-rendered.yaml"
previous_manifest="${TMPDIR:-/tmp}/doops-upgrade-previous-manifest.yaml"
stale_mounts="${TMPDIR:-/tmp}/doops-upgrade-stale-mounts.json"
remove_mounts_patch="${TMPDIR:-/tmp}/doops-upgrade-remove-mounts.json"
mount_backup="${TMPDIR:-/tmp}/doops-upgrade-mount-backup.json"
helm template "$release" "$chart" -n "$ns" -f "$values" >"$rendered_manifest"
helm get manifest "$release" -n "$ns" --revision "$previous" >"$previous_manifest"
python3 - "$ns" "$release" "$workload" "$previous" "$rendered_manifest" "$previous_manifest" "$live_deployment" "$stale_mounts" "$remove_mounts_patch" "$mount_backup" <<'PY'
import json, os, sys, yaml

(namespace, release, workload, revision, rendered_path, previous_path, live_path,
 stale_path, patch_path, backup_path) = sys.argv[1:11]


def deployment_volumes(path, label):
    with open(path, encoding="utf-8") as fh:
        docs = [doc for doc in yaml.safe_load_all(fh) if doc and doc.get("kind") == "Deployment"
                and (doc.get("metadata") or {}).get("name") == workload]
    if len(docs) != 1:
        raise SystemExit("stale_volume_mounts_unresolved: the " + label + " must render exactly one " + workload + " Deployment")
    pod = ((docs[0].get("spec") or {}).get("template") or {}).get("spec") or {}
    return {volume.get("name") for volume in pod.get("volumes") or []}


deleted = deployment_volumes(previous_path, "previous revision") - deployment_volumes(rendered_path, "new chart")
with open(live_path, encoding="utf-8") as fh:
    live_pod = ((json.load(fh).get("spec") or {}).get("template") or {}).get("spec") or {}

stale = []
for group in ("initContainers", "containers"):
    for container_index, container in enumerate(live_pod.get(group) or []):
        for mount_index, mount in enumerate(container.get("volumeMounts") or []):
            if mount.get("name") in deleted:
                stale.append({"group": group, "container": container.get("name"),
                              "containerIndex": container_index, "mountIndex": mount_index, "mount": mount})

for path in (stale_path, patch_path, backup_path):
    if os.path.exists(path):
        os.unlink(path)
if not stale:
    print("stale_volume_mounts=none")
    raise SystemExit(0)

# Each removal is guarded by a test of the container and mount it expects, so a
# Deployment that changed since the snapshot rejects the whole patch instead of
# losing a different mount. Removals run from the highest index down so earlier
# indices stay valid.
ops = []
for item in stale:
    base = "/spec/template/spec/%s/%d" % (item["group"], item["containerIndex"])
    ops.append({"op": "test", "path": base + "/name", "value": item["container"]})
    ops.append({"op": "test", "path": "%s/volumeMounts/%d/name" % (base, item["mountIndex"]),
                "value": item["mount"].get("name")})
for item in sorted(stale, key=lambda item: (item["group"], item["containerIndex"], -item["mountIndex"])):
    ops.append({"op": "remove", "path": "/spec/template/spec/%s/%d/volumeMounts/%d"
                % (item["group"], item["containerIndex"], item["mountIndex"])})

with open(stale_path, "w", encoding="utf-8") as fh:
    json.dump(stale, fh)
with open(patch_path, "w", encoding="utf-8") as fh:
    json.dump(ops, fh, separators=(",", ":"))
with open(backup_path, "w", encoding="utf-8") as fh:
    json.dump({"apiVersion": "v1", "kind": "ConfigMap",
               "metadata": {"name": "doops-upgrade-mount-backup-" + release, "namespace": namespace},
               "data": {"workload": workload, "revision": revision,
                        "volumeMounts.json": json.dumps(stale, sort_keys=True)}}, fh)
print("stale_volume_mounts=" + ",".join(
    "%s:%s:%s" % (item["container"], item["mount"].get("name"), item["mount"].get("mountPath", ""))
    for item in stale))
PY
`

const selfUpgradeRestoreVolumeMounts = `restore_volume_mounts() {
  python3 - "$ns" "$workload" "$stale_mounts" <<'PY'
import json, subprocess, sys

namespace, workload, stale_path = sys.argv[1:4]
with open(stale_path, encoding="utf-8") as fh:
    stale = json.load(fh)
live = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "deployment", workload, "-o", "json"], timeout=30,
))
pod = ((live.get("spec") or {}).get("template") or {}).get("spec") or {}
ops = []
for item in stale:
    containers = pod.get(item["group"]) or []
    index = next((i for i, container in enumerate(containers) if container.get("name") == item["container"]), None)
    if index is None:
        raise SystemExit("container " + str(item["container"]) + " is missing after the rollback")
    mount = item["mount"]
    mounts = containers[index].get("volumeMounts")
    if any(m.get("name") == mount.get("name") and m.get("mountPath") == mount.get("mountPath") for m in mounts or []):
        continue
    base = "/spec/template/spec/%s/%d" % (item["group"], index)
    ops.append({"op": "test", "path": base + "/name", "value": item["container"]})
    if mounts is None:
        ops.append({"op": "add", "path": base + "/volumeMounts", "value": [mount]})
        containers[index]["volumeMounts"] = [mount]
    else:
        ops.append({"op": "add", "path": base + "/volumeMounts/-", "value": mount})
        mounts.append(mount)
if ops:
    subprocess.run(["kubectl", "-n", namespace, "patch", "deployment", workload, "--type=json",
                    "-p", json.dumps(ops, separators=(",", ":"))], check=True, timeout=60)
print("volume_mounts_restored count=" + str(sum(1 for op in ops if op["op"] == "add")))
PY
}
`

const selfUpgradePreflightScript = selfUpgradeKubeconfigPrelude + selfUpgradeDeploymentVerifier + "verify_record=\n" + selfUpgradeApplyChecks + `failure_category=helm_dry_run_failed
helm upgrade "$release" "$chart" -n "$ns" -f "$values" --timeout 180s --wait=false --dry-run=server >/dev/null
echo "preflight-passed image=${target}"
`

const selfUpgradePullOnlyScript = selfUpgradeKubeconfigPrelude + `echo "preflight-passed image=${DOOPS_UPGRADE_IMAGE}"
`

const selfUpgradeApplyScript = selfUpgradeKubeconfigPrelude + selfUpgradeDeploymentVerifier + selfUpgradeApplyChecks + selfUpgradeRestoreVolumeMounts + `mutated=false
rollback_release() {
  echo "rolling back ${release} to ${previous}" >&2
  if ! helm rollback "$release" "$previous" -n "$ns" --timeout 300s --wait; then
    echo "Error: automatic rollback failed: Helm rollback command failed" >&2
    return 1
  fi
  if [ -s "$stale_mounts" ] && ! restore_volume_mounts; then
    echo "Error: automatic rollback failed: the removed volume mounts could not be restored" >&2
    return 1
  fi
  if ! kubectl -n "$ns" rollout status "deployment/${workload}" --timeout 300s; then
    echo "Error: automatic rollback failed: rolled-back Deployment did not become ready" >&2
    return 1
  fi
  if ! DOOPS_UPGRADE_VERIFICATION_STAGE=rollback verify_deployment "$previous_image" baseline record; then
    echo "Error: automatic rollback failed: rolled-back Deployment final state is not verified" >&2
    return 1
  fi
  echo "automatic rollback complete revision=${previous}" >&2
}
cleanup() {
  status=$?
  trap - EXIT
  if [ "$status" -ne 0 ] && [ "$mutated" = true ]; then
    if ! rollback_release; then
      echo "Error: automatic rollback failed; manual intervention is required" >&2
      printf '%s' automatic_rollback_failed >"${TMPDIR:-/tmp}/doops-upgrade-category"
      export DOOPS_UPGRADE_ROLLBACK_OUTCOME=failed
      finish_upgrade 1
    fi
    export DOOPS_UPGRADE_ROLLBACK_OUTCOME=succeeded
  fi
  finish_upgrade "$status"
}
trap cleanup EXIT
failure_category=helm_dry_run_failed
helm upgrade "$release" "$chart" -n "$ns" -f "$values" --timeout 180s --wait=false --dry-run=server >/dev/null
if [ -s "$stale_mounts" ]; then
  # Back the mounts up in the cluster before the first change, so the evidence
  # survives this Job even when the rollback cannot run.
  failure_category=mount_backup_failed
  kubectl -n "$ns" apply -f "$mount_backup"
fi
mutated=true
if [ -s "$stale_mounts" ]; then
  failure_category=mount_removal_failed
  kubectl -n "$ns" patch deployment "$workload" --type=json -p "$(cat "$remove_mounts_patch")"
  echo "stale_volume_mounts_removed workload=${workload}"
fi
failure_category=helm_upgrade_failed
helm upgrade "$release" "$chart" -n "$ns" -f "$values" --timeout 180s --wait=false >/dev/null
failure_category=release_verification_failed
deadline=$(( $(date +%s) + 600 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if verify_deployment "$target"; then
    sleep 15
    if verify_deployment "$target" controller record; then
      emit_upgrade_event succeeded "" || { failure_category=verification_receipt_failed; exit 1; }
      echo "self-upgrade complete image=${target}"
      exit 0
    fi
  fi
  sleep 5
done
echo "new deployment did not stay ready" >&2
exit 1
`

const selfUpgradeRollbackScript = selfUpgradeKubeconfigPrelude + selfUpgradeDeploymentVerifier + selfUpgradeRevisionImage + selfUpgradeRBACGuard + `previous="$DOOPS_UPGRADE_REVISION"
failure_category=rollback_evidence_invalid
python3 - "$ns" "$release" "$previous" <<'PY'
import json, subprocess, sys
ns, release, revision = sys.argv[1:4]
rows = json.loads(subprocess.check_output(["helm", "history", release, "-n", ns, "-o", "json"], timeout=30))
current = [int(row["revision"]) for row in rows if row["status"] == "deployed"]
if len(current) != 1 or not any(int(row["revision"]) == int(revision) < current[0] and row["status"] == "superseded" for row in rows):
    raise SystemExit("admitted rollback revision is no longer eligible")
PY
expected="$(revision_image "$previous")"
test "$expected" = "$DOOPS_UPGRADE_IMAGE" || { echo "rollback revision image changed after admission" >&2; exit 1; }
DOOPS_UPGRADE_BASELINE="$(python3 - "$ns" "$release" "$workload" "$previous" <<'PY'
import json, os, re, subprocess, sys
sys.path.insert(0, "/app")
from adopt_agent import report_failure
ns, release, workload, revision = sys.argv[1:]
stored = json.loads(subprocess.check_output(
    ["kubectl", "-n", ns, "get", "configmap", "doops-upgrade-verified-" + release, "-o", "json"], timeout=30))
receipt = json.loads(stored["data"]["evidence.json"])
entry = receipt["revisions"][revision]
if receipt.get("release") != release or receipt.get("workload") != workload or receipt.get("schemaVersion") != 1:
    raise SystemExit("category=rollback_evidence_invalid rollback receipt scope changed")
if (entry.get("outcome") != "succeeded" or not re.fullmatch(r"[0-9a-f]{40}", entry.get("release_commit", ""))
        or entry["release_commit"] != os.environ["DOOPS_UPGRADE_EXPECTED_RELEASE_COMMIT"]
        or entry["image"] != os.environ["DOOPS_UPGRADE_IMAGE"]):
    report_failure("rollback_commit_unknown")
    raise SystemExit("category=rollback_commit_unknown historical verified commit changed or is unknown")
if not re.fullmatch(r"sha256:[0-9a-f]{64}", entry.get("runtimeDigest", "")) or not entry.get("node"):
    raise SystemExit("category=rollback_evidence_invalid historical runtime identity is missing")
print(json.dumps({"image": entry["image"], "digest": entry["runtimeDigest"], "node": entry["node"],
                  "runtime_class": entry.get("runtimeClass", ""), "release_commit": entry["release_commit"]}))
PY
)"
export DOOPS_UPGRADE_BASELINE
current_manifest="${TMPDIR:-/tmp}/doops-rollback-current-manifest.yaml"
rollback_manifest="${TMPDIR:-/tmp}/doops-rollback-target-manifest.yaml"
helm get manifest "$release" -n "$ns" >"$current_manifest"
helm get manifest "$release" -n "$ns" --revision "$previous" >"$rollback_manifest"
failure_category=rbac_controller_identity_changed
verify_controller_rbac "$current_manifest" "$rollback_manifest"
failure_category=helm_rollback_failed
helm rollback "$release" "$previous" -n "$ns" --timeout 300s --wait
failure_category=rollback_rollout_failed
kubectl -n "$ns" rollout status "deployment/${workload}" --timeout 300s
failure_category=rollback_verification_failed
verify_deployment "$expected" baseline record
echo "self-upgrade rolled back to ${previous}"
`

const selfUpgradeAdoptScript = selfUpgradeKubeconfigPrelude + `failure_category=adopt_failed
python3 /app/adopt_agent.py run \
  --namespace "$ns" --workload "$workload" --release "$release" \
  --cluster "$DOOPS_GATEWAY_CLUSTER" --instance "$DOOPS_GATEWAY_INSTANCE" \
  --image "$DOOPS_UPGRADE_IMAGE"
echo "self-upgrade complete image=${DOOPS_UPGRADE_IMAGE}"
`
