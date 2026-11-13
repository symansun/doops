#!/usr/bin/env python3
"""Bring an unmanaged doops-agent Deployment under the doops-agent Helm Chart.

  plan     preflight only; prints what run would do.
  run      back up, mark for Helm, helm install, verify the new Pod, and
           restore the backup on any failure.
  restore  put the backed-up Deployment back and drop the Helm release record.

The adopted Agent receives its model provider from the Gateway; no settings are copied.
"""
import argparse
import base64
import datetime
import hashlib
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
DROPPED_MOUNTS = {SETTINGS_MOUNT, "/scripts", "/root/.kube", "/root/.kube/config"}
VOLATILE = ("resourceVersion", "uid", "generation", "creationTimestamp", "managedFields", "selfLink")
ADOPTED_LABEL = "doops.sh/adopted-by"
PINNED_IMAGE = re.compile(r"^([a-z0-9.:/_-]+)@(sha256:[0-9a-f]{64})$")


class AdoptError(Exception):
    def __init__(self, message, category="adopt_preflight_failed"):
        self.category = category
        super().__init__(f"category={category} {message}")


def evidence_context(namespace, workload, release, image):
    context = {key: os.environ.get("DOOPS_UPGRADE_" + key.upper(), "") for key in (
        "requester_id", "session_id", "audit_id",
        "expected_release_commit", "source_commit", "ci_url", "phase", "job", "created_at",
    )}
    context["upgrade_operation_id"] = os.environ.get("DOOPS_UPGRADE_OPERATION_ID", "")
    context.update(namespace=namespace, workload=workload, release=release, image=image,
                   digest=image.rsplit("@", 1)[1] if "@sha256:" in image else "",
                   time=datetime.datetime.now(datetime.timezone.utc).isoformat())
    return context


def verified_release_commit(namespace, pod, expected=None):
    """Read the running Pod, never substitute the requested commit for evidence."""
    try:
        version = run(["kubectl", "-n", namespace, "exec", pod, "-c", CONTAINER,
                       "--", "/app/doops-agent", "-version"], timeout=30)
        run(["kubectl", "-n", namespace, "exec", pod, "-c", CONTAINER,
             "--", "curl", "-fsS", "--max-time", "5", "http://127.0.0.1:42222/health/ready"], timeout=30)
    except (AdoptError, OSError, subprocess.SubprocessError) as exc:
        raise AdoptError("cannot read Agent release and Gateway readiness", "release_probe_failed") from exc
    match = re.fullmatch(r"release=\S+ commit=([0-9a-f]{40})\s*", version)
    if not match:
        raise AdoptError("Agent reported an unknown release commit", "release_commit_unknown")
    actual = match.group(1)
    if expected is not None:
        if not re.fullmatch(r"[0-9a-f]{40}", expected):
            raise AdoptError("expected release commit is unknown", "release_commit_unknown")
        if actual != expected:
            raise AdoptError(f"Agent commit {actual} differs from expected {expected}", "release_commit_mismatch")
    return actual


def write_verified_receipt(namespace, release, workload, evidence):
    if evidence.get("outcome") != "succeeded" or not re.fullmatch(r"[0-9a-f]{40}", evidence.get("release_commit", "")):
        raise AdoptError("cannot persist unverified release identity", "verification_receipt_invalid")
    try:
        rows = json.loads(run(["helm", "history", release, "-n", namespace, "-o", "json"], timeout=30))
        deployed = [int(row["revision"]) for row in rows if row["status"] == "deployed"]
        if len(deployed) != 1:
            raise AdoptError("receipt requires one deployed Helm revision", "verification_receipt_invalid")
        name = "doops-upgrade-verified-" + release
        raw = run(["kubectl", "-n", namespace, "get", "configmap", name, "--ignore-not-found", "-o", "json"], timeout=30).strip()
        receipt = json.loads(json.loads(raw)["data"]["evidence.json"]) if raw else {
            "schemaVersion": 1, "release": release, "workload": workload, "revisions": {},
        }
        if receipt.get("schemaVersion") != 1 or receipt.get("release") != release or receipt.get("workload") != workload:
            raise AdoptError("receipt scope mismatch", "verification_receipt_invalid")
        evidence = dict(evidence, revision=deployed[0])
        evidence["verified_at"] = evidence.get("time") or datetime.datetime.now(datetime.timezone.utc).isoformat()
        key = str(deployed[0])
        previous = receipt["revisions"].get(key)
        if previous and previous.get("release_commit"):
            if any(previous.get(field) != evidence.get(field) for field in ("image", "release_commit")):
                raise AdoptError("historical revision identity changed", "verification_receipt_invalid")
        else:
            receipt["revisions"][key] = evidence
        # Keep prior revisions even when Helm history has been compacted, and do
        # not reattribute an old revision to the operator verifying it today.
        receipt["last_verification"] = evidence
        manifest = {"apiVersion": "v1", "kind": "ConfigMap",
                    "metadata": {"name": name, "namespace": namespace},
                    "data": {"evidence.json": json.dumps(receipt, sort_keys=True)}}
        run(["kubectl", "-n", namespace, "apply", "-f", "-"], stdin=json.dumps(manifest), timeout=30)
        save_verification(evidence)
    except AdoptError as exc:
        if exc.category == "verification_receipt_invalid":
            raise
        raise AdoptError("verification receipt persistence failed", "verification_receipt_failed") from exc
    except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError) as exc:
        raise AdoptError("verification receipt persistence failed", "verification_receipt_failed") from exc


def save_verification(evidence):
    path = os.path.join(os.environ.get("TMPDIR", "/tmp"), "doops-upgrade-verification.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(evidence, fh, sort_keys=True)


def write_operation_receipt(outcome, category=""):
    """Keep terminal evidence independently of Job/Pod TTL and Helm history."""
    namespace = os.environ["DOOPS_UPGRADE_NAMESPACE"]
    record = evidence_context(namespace, os.environ["DOOPS_UPGRADE_WORKLOAD"],
                              os.environ["DOOPS_UPGRADE_RELEASE"], os.environ["DOOPS_UPGRADE_IMAGE"])
    job = record["job"]
    identity = hashlib.sha256(record["upgrade_operation_id"].encode()).hexdigest()[:24]
    if record["phase"] not in ("preflight", "apply", "rollback") or job != f"doops-self-upgrade-{identity}-{record['phase']}":
        raise AdoptError("operation receipt identity is invalid", "verification_receipt_invalid")
    path = os.path.join(os.environ.get("TMPDIR", "/tmp"), "doops-upgrade-verification.json")
    if os.path.exists(path):
        with open(path, encoding="utf-8") as fh:
            verified = json.load(fh)
        if verified.get("job") != job or verified.get("upgrade_operation_id") != record["upgrade_operation_id"]:
            raise AdoptError("verification belongs to another operation", "verification_receipt_invalid")
        record["verification"] = verified
    if outcome == "succeeded" and record["phase"] != "preflight":
        verified = record.get("verification", {})
        if (verified.get("image") != record["image"] or verified.get("outcome") != "succeeded"
                or verified.get("release_commit") != record["expected_release_commit"]
                or not re.fullmatch(r"[0-9a-f]{40}", verified.get("release_commit", ""))):
            raise AdoptError("successful operation requires verified runtime evidence", "verification_receipt_invalid")
    record.update(schemaVersion=1, outcome=outcome, category=category,
                  rollback_outcome=os.environ.get("DOOPS_UPGRADE_ROLLBACK_OUTCOME", ""))
    name = job.replace("doops-self-upgrade-", "doops-upgrade-receipt-", 1)
    manifest = {"apiVersion": "v1", "kind": "ConfigMap",
                "metadata": {"name": name, "namespace": namespace,
                             "annotations": {"doops.sh/upgrade-operation-id": record["upgrade_operation_id"],
                                             "doops.sh/job": job}},
                "data": {"evidence.json": json.dumps(record, sort_keys=True)}}
    try:
        run(["kubectl", "-n", namespace, "apply", "-f", "-"], stdin=json.dumps(manifest), timeout=30)
    except (AdoptError, OSError, subprocess.SubprocessError) as exc:
        raise AdoptError("operation receipt persistence failed", "verification_receipt_failed") from exc
    # The shell must not rewrite a child's terminal result: environment changes
    # (notably an adoption restore outcome) cannot propagate to its parent.
    with open(os.path.join(os.environ.get("TMPDIR", "/tmp"), "doops-upgrade-terminal.json"), "w", encoding="utf-8") as fh:
        json.dump(record, fh, sort_keys=True)
    return record


def report_failure(category):
    # The shell controller reads this small category file when its EXIT trap
    # reports the terminal result, preserving a specific verifier failure.
    with open(os.path.join(os.environ.get("TMPDIR", "/tmp"), "doops-upgrade-category"), "w", encoding="utf-8") as fh:
        fh.write(category)


def evidence_exception_hook(kind, exc, traceback):
    category = exc.category if isinstance(exc, AdoptError) else "release_verification_failed"
    report_failure(category)
    print(f"category={category} verification failed", file=sys.stderr)


def run(args, stdin=None, timeout=120):
    result = subprocess.run(args, input=stdin, capture_output=True, text=True, timeout=timeout)
    if result.returncode != 0:
        # API diagnostics can echo submitted objects, including Secret data.
        raise AdoptError(f"{args[0]} command failed", "cluster_command_failed")
    return result.stdout


def kget(namespace, *args):
    return json.loads(run(["kubectl", "-n", namespace, "get", *args, "-o", "json"]))


def backup_name(workload):
    operation = os.environ.get("DOOPS_UPGRADE_OPERATION_ID", "")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,127}", operation):
        raise AdoptError("upgrade_operation_id is required for the backup", "adopt_backup_failed")
    identity = hashlib.sha256((workload + ":" + operation).encode()).hexdigest()[:24]
    return "doops-adopt-backup-" + identity


def agent_container(deployment):
    for container in deployment["spec"]["template"]["spec"].get("containers") or []:
        if container.get("name") == CONTAINER:
            return container
    raise AdoptError("deployment has no doops-agent container")


def env_map(container):
    return {e["name"]: e.get("value", "") for e in container.get("env") or [] if "valueFrom" not in e}


ENROLL_TOKEN_ENV = "DOOPS_AGENT_ENROLL_TOKEN"


def enrollment_token_ref(container):
    """Return the Secret reference for the Agent enrollment token, if any.

    The Chart only renders the token from a Secret. A token passed any other
    way would be dropped by adoption and could strand the Agent once its
    target enforces enrollment, so adoption refuses it instead.
    """
    for arg in list(container.get("command") or []) + list(container.get("args") or []):
        if str(arg).lstrip("-").split("=", 1)[0] == "enroll-token":
            raise AdoptError("adoption cannot carry an -enroll-token argument; "
                             "move the token into a Secret referenced by " + ENROLL_TOKEN_ENV)
    for entry in container.get("env") or []:
        if entry.get("name") != ENROLL_TOKEN_ENV:
            continue
        ref = (entry.get("valueFrom") or {}).get("secretKeyRef")
        if not ref or not ref.get("name") or "value" in entry:
            raise AdoptError(ENROLL_TOKEN_ENV + " must come from a Secret (valueFrom.secretKeyRef) to be adopted")
        return {"enrollmentTokenSecret": ref["name"], "enrollmentTokenKey": ref.get("key") or "token"}
    return {}


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
    validate_deployment(deployment, cluster, instance)
    return deployment


def validate_deployment(deployment, cluster, instance):
    """Check that a Deployment can be rendered by the Chart."""
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
    if not env.get("DOOPS_GATEWAY_URL"):
        raise AdoptError("deployment env DOOPS_GATEWAY_URL is required")
    enrollment_token_ref(container)


def chart_values(deployment, workload, repository, digest):
    spec = deployment["spec"]
    pod = spec["template"]["spec"]
    container = agent_container(deployment)
    env = env_map(container)
    # A legacy model settings mount is dropped: the adopted Agent receives its
    # model provider from the Gateway.
    dropped = {m["name"] for m in container.get("volumeMounts") or [] if m.get("mountPath") in DROPPED_MOUNTS}
    dropped.add("doagent-config")
    mounts = [m for m in container.get("volumeMounts") or [] if m["name"] not in dropped]
    kept = {m["name"] for m in mounts}
    return {
        "image": {"repository": repository, "digest": digest, "pullPolicy": "IfNotPresent"},
        "workload": {"name": workload, "selectorLabels": spec["selector"]["matchLabels"]},
        "imagePullSecrets": [{"name": s["name"]} for s in pod.get("imagePullSecrets") or []],
        "serviceAccount": {"name": service_account(pod)},
        "kubeconfig": {"source": "serviceAccount"},
        "gateway": {
            "url": env["DOOPS_GATEWAY_URL"],
            "cluster": env["DOOPS_GATEWAY_CLUSTER"],
            "instance": env["DOOPS_GATEWAY_INSTANCE"],
            **enrollment_token_ref(container),
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


def write_backup(namespace, workload, deployment, baseline):
    """Back up the Deployment; any legacy settings object is left untouched."""
    name = backup_name(workload)
    attribution = evidence_context(namespace, workload, os.environ.get("DOOPS_UPGRADE_RELEASE", ""),
                                   agent_container(deployment)["image"])
    backup = {
        "apiVersion": "v1",
        "kind": "Secret",
        "type": "Opaque",
        "immutable": True,
        "metadata": {"name": name, "namespace": namespace, "labels": {ADOPTED_LABEL: "doops-upgrade"},
                     "annotations": {"doops.sh/" + key.replace("_", "-"): str(value)
                                     for key, value in attribution.items()}},
        "data": {
            "deployment.json": base64.b64encode(json.dumps(clean_object(json.loads(json.dumps(deployment))), sort_keys=True).encode()).decode(),
            "baseline.json": base64.b64encode(json.dumps(baseline, sort_keys=True).encode()).decode(),
        },
    }
    # create avoids last-applied plaintext annotations and refuses to replace
    # evidence from an earlier attempt of this operation.
    try:
        run(["kubectl", "-n", namespace, "create", "-f", "-"], stdin=json.dumps(backup))
    except (AdoptError, OSError, subprocess.SubprocessError) as exc:
        raise AdoptError("cannot persist the complete rollback backup Secret", "adopt_backup_failed") from exc
    return name


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


def runtime_digest_of(pod, container=CONTAINER):
    for status in (pod.get("status") or {}).get("containerStatuses") or []:
        digest = str(status.get("imageID") or "").split("://")[-1].rsplit("@", 1)[-1]
        if status.get("name") == container and re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            return digest
    return ""


def controller_runtime_digest(namespace, image):
    """The adoption Job runs the target image on the cluster runtime, so its own
    imageID is the runtime digest to expect; an OCI index digest can differ."""
    name = os.environ.get("HOSTNAME", "")
    if not name:
        raise AdoptError("controller Pod name is missing", "controller_identity_unverified")
    try:
        pod = kget(namespace, "pod", name)
    except (AdoptError, OSError, subprocess.SubprocessError, ValueError) as exc:
        raise AdoptError("controller Pod could not be read", "controller_identity_unverified") from exc
    spec = pod.get("spec") or {}
    container = next((c for c in spec.get("containers") or [] if c.get("name") == "controller"), {})
    ready = any(c.get("name") == "controller" and c.get("ready")
                for c in (pod.get("status") or {}).get("containerStatuses") or [])
    digest = runtime_digest_of(pod, "controller")
    if not digest or not ready or not spec.get("nodeName") or container.get("image") != image:
        raise AdoptError("controller runtime identity is not verified", "controller_identity_unverified")
    return digest


def verify(namespace, workload, image, deadline_seconds=600, interval=5, clock=time.time, sleep=time.sleep,
           runtime_digest=None, expected_commit=None):
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
            ready = [p for p in pods if (p.get("status") or {}).get("phase") == "Running"
                     and any(c.get("type") == "Ready" and c.get("status") == "True"
                             for c in (p.get("status") or {}).get("conditions") or [])
                     and any(c.get("name") == CONTAINER and c.get("image") == image
                             for c in (p.get("spec") or {}).get("containers") or [])
                     and any(c.get("name") == CONTAINER and c.get("ready")
                             for c in (p.get("status") or {}).get("containerStatuses") or [])
                     and runtime_digest_of(p) == expected]
            if ready and len(pods) == len(ready):
                commits = {verified_release_commit(namespace, p["metadata"]["name"], expected_commit) for p in ready}
                if len(commits) != 1:
                    raise AdoptError("Agent Pods report different commits", "release_commit_mismatch")
                pod = ready[0]
                if not pod["metadata"].get("uid") or not pod["spec"].get("nodeName"):
                    raise AdoptError("Pod UID or node is missing", "release_verification_failed")
                return {"podUID": pod["metadata"]["uid"], "release_commit": commits.pop(),
                        "runtimeDigest": runtime_digest_of(pod), "node": pod["spec"]["nodeName"],
                        "runtimeClass": pod["spec"].get("runtimeClassName", "")}
            last = "Pod runtime digest does not match the target"
        sleep(interval)
    raise AdoptError(f"verification failed: {last}", "release_verification_failed")


def restore(namespace, workload, release, baseline=None):
    backup = kget(namespace, "secret", backup_name(workload))
    deployment = json.loads(base64.b64decode(backup["data"]["deployment.json"], validate=True))
    if baseline is None:
        baseline = json.loads(base64.b64decode(backup["data"]["baseline.json"], validate=True))
    if (deployment["metadata"].get("name") != workload or deployment["metadata"].get("namespace") != namespace
            or not re.fullmatch(r"[0-9a-f]{40}", baseline.get("release_commit", ""))
            or not re.fullmatch(r"sha256:[0-9a-f]{64}", baseline.get("runtimeDigest", ""))):
        raise AdoptError("restore backup scope or verified identity is invalid", "adopt_restore_failed")
    # Drop the Helm record without letting Helm delete the restored workload.
    for secret in kget(namespace, "secrets", "-l", f"owner=helm,name={release}").get("items") or []:
        run(["kubectl", "-n", namespace, "delete", "secret", secret["metadata"]["name"], "--ignore-not-found"])
    run(["kubectl", "-n", namespace, "replace", "--force", "-f", "-"], stdin=json.dumps(deployment), timeout=180)
    run(["kubectl", "-n", namespace, "rollout", "status", f"deployment/{workload}", "--timeout", "300s"], timeout=320)
    image = agent_container(deployment)["image"]
    evidence = verify(namespace, workload, image,
                      runtime_digest=baseline["runtimeDigest"], expected_commit=baseline["release_commit"])
    evidence.update(evidence_context(namespace, workload, release, image), phase="rollback", outcome="succeeded",
                    category="", expected_release_commit=baseline["release_commit"],
                    verified_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                    verification_stage="rollback")
    save_verification(evidence)
    return evidence


def adopt(namespace, workload, release, cluster, instance, image, chart=CHART, plan_only=False, verifier=verify):
    repository, digest = parse_image(image)
    deployment = preflight(namespace, workload, release, cluster, instance)
    values = chart_values(deployment, workload, repository, digest)
    if plan_only:
        return {"phase": "plan", "release": release, "workload": workload, "image": image,
                "serviceAccount": values["serviceAccount"]["name"], "backup": backup_name(workload),
                "keptVolumes": [v["name"] for v in values["volumes"]]}
    expected = os.environ.get("DOOPS_UPGRADE_EXPECTED_RELEASE_COMMIT", "")
    if not re.fullmatch(r"[0-9a-f]{40}", expected):
        raise AdoptError("expected release commit is required", "release_commit_unknown")
    old_image = agent_container(deployment)["image"]
    # A verified baseline is required even for an unmanaged workload; otherwise
    # restoration could not prove which version was restored.
    selector = ",".join(f"{k}={v}" for k, v in sorted(deployment["spec"]["selector"]["matchLabels"].items()))
    pods = [p for p in kget(namespace, "pods", "-l", selector).get("items") or []
            if not p["metadata"].get("deletionTimestamp")]
    if len(pods) != 1 or not runtime_digest_of(pods[0]):
        raise AdoptError("unmanaged baseline runtime is unknown", "rollback_commit_unknown")
    baseline = verifier(namespace, workload, old_image, runtime_digest=runtime_digest_of(pods[0]))
    write_backup(namespace, workload, deployment, baseline)
    category = "adopt_mutation_failed"
    try:
        mark_for_helm(namespace, workload, release)
        helm_install(namespace, release, values, chart, dry_run=True)
        helm_install(namespace, release, values, chart)
        category = "release_verification_failed"
        evidence = verifier(namespace, workload, image, runtime_digest=controller_runtime_digest(namespace, image),
                            expected_commit=expected)
        time.sleep(15)
        evidence = verifier(namespace, workload, image, runtime_digest=controller_runtime_digest(namespace, image),
                            expected_commit=expected)
        evidence.update(evidence_context(namespace, workload, release, image), outcome="succeeded", category="")
        category = "verification_receipt_failed"
        write_verified_receipt(namespace, release, workload, evidence)
        write_operation_receipt("succeeded")
    except Exception as exc:
        try:
            restore(namespace, workload, release, baseline)
            os.environ["DOOPS_UPGRADE_ROLLBACK_OUTCOME"] = "succeeded"
        except Exception as restore_exc:
            os.environ["DOOPS_UPGRADE_ROLLBACK_OUTCOME"] = "failed"
            raise AdoptError("automatic restore failed", "adopt_restore_failed") from restore_exc
        raise AdoptError("adoption failed; backup restored", exc.category if isinstance(exc, AdoptError) else category) from exc
    return evidence


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
            print(json.dumps(restore(args.namespace, args.workload, args.release), sort_keys=True))
            return 0
        if not args.cluster or not args.instance:
            raise AdoptError("--cluster and --instance are required")
        result = adopt(args.namespace, args.workload, args.release, args.cluster, args.instance,
                       args.image, chart=args.chart, plan_only=args.action == "plan")
        print(json.dumps(result, sort_keys=True))
        return 0
    except (AdoptError, OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError) as exc:
        category = exc.category if isinstance(exc, AdoptError) else "adopt_failed"
        report_failure(category)
        evidence = evidence_context(args.namespace, args.workload, args.release, args.image)
        evidence.update(outcome="failed", category=category)
        if args.action == "run":
            try:
                evidence = write_operation_receipt("failed", category)
            except (AdoptError, OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError):
                report_failure("verification_receipt_failed")
                evidence["category"] = "verification_receipt_failed"
        print(json.dumps(evidence, sort_keys=True), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
