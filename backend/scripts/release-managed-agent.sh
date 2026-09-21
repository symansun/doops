#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Release a Helm-managed doops-agent. This is the only Agent install/upgrade path.

Helm chart deploy/helm/doops-agent starts the replacement Pod first
(maxSurge=1, maxUnavailable=0). The live Agent keeps serving until the new Pod
is Ready and has replaced the Gateway session. kubectl set-image, docker
restart, Jobs, release-runners, and doops upgrade apply are not release
methods.

Existing cluster (DoOps target already online):

  bash scripts/release-managed-agent.sh \
    --target gw-edu-coder \
    --session agent-release-v1.3.0 \
    --cluster doops-edu \
    --instance edu-coder \
    --release doops-agent-live \
    --namespace kz-ops \
    --kubeconfig /secure/path/to/kubeconfig \
    --proxy-allowed-hosts 127.0.0.1,internal.example.com \
    --host-alias internal.example.com=10.0.0.10 \
    --version v1.3.0

New cluster (operator kubeconfig, complete values, no Agent yet):

  bash scripts/release-managed-agent.sh \
    --release doops-agent \
    --namespace doops-system \
    --values /path/to/complete-values.yaml \
    --version v1.3.0

Required:
  --release NAME
  --namespace NAME
  --image REPO@sha256:<digest>   or  --version vX.Y.Z

Existing cluster also requires:
  --target NAME
  --session NAME
  --cluster NAME
  --instance NAME
  --kubeconfig PATH (or KUBECONFIG)

New cluster also requires:
  --values FILE

Options:
  --chart-dir PATH     Default: deploy/helm/doops-agent
  --kubeconfig PATH    External Helm controller config. Default: $KUBECONFIG
  --timeout DURATION   Helm wait timeout for new-cluster installs. Default: 5m
  --handoff-timeout SEC
                       Seconds to wait for the live image to become the digest.
                       Default: 600.
  --proxy-allowed-hosts HOSTS
                       Comma-separated exact upstream hostnames/IPs. Existing
                       clusters only; omitted preserves the live Helm value.
  --host-alias HOST=IP
                       Add or replace one exact Pod hostname mapping. Existing
                       clusters only; repeat for multiple hostnames. Omitted
                       preserves the live Helm value.
  --dry-run            Preflight only
  -h, --help
EOF
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=doops-release-image.sh
source "${SCRIPT_DIR}/doops-release-image.sh"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TARGET=""
SESSION=""
CLUSTER=""
INSTANCE=""
RELEASE=""
NAMESPACE=""
VALUES=""
VERSION=""
IMAGE=""
CHART_DIR="${REPO_ROOT}/deploy/helm/doops-agent"
KUBECONFIG_PATH="${KUBECONFIG:-}"
TIMEOUT="5m"
HANDOFF_TIMEOUT="600"
PROXY_ALLOWED_HOSTS=""
PROXY_ALLOWED_HOSTS_SET=false
HOST_ALIAS_ARGS=()
HOST_ALIASES_JSON="{}"
HOST_ALIASES_SET=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      TARGET="${2:-}"
      shift 2
      ;;
    --session)
      SESSION="${2:-}"
      shift 2
      ;;
    --cluster)
      CLUSTER="${2:-}"
      shift 2
      ;;
    --instance)
      INSTANCE="${2:-}"
      shift 2
      ;;
    --release)
      RELEASE="${2:-}"
      shift 2
      ;;
    --namespace)
      NAMESPACE="${2:-}"
      shift 2
      ;;
    --values)
      VALUES="${2:-}"
      shift 2
      ;;
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --image)
      IMAGE="${2:-}"
      shift 2
      ;;
    --chart-dir)
      CHART_DIR="${2:-}"
      shift 2
      ;;
    --kubeconfig)
      KUBECONFIG_PATH="${2:-}"
      shift 2
      ;;
    --timeout)
      TIMEOUT="${2:-}"
      shift 2
      ;;
    --handoff-timeout)
      HANDOFF_TIMEOUT="${2:-}"
      shift 2
      ;;
    --proxy-allowed-hosts)
      PROXY_ALLOWED_HOSTS="${2:-}"
      PROXY_ALLOWED_HOSTS_SET=true
      shift 2
      ;;
    --host-alias)
      HOST_ALIAS_ARGS+=("${2:-}")
      HOST_ALIASES_SET=true
      shift 2
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_value() {
  local name="$1"
  local value="$2"
  if [[ -z "${value}" ]]; then
    echo "Error: ${name} is required" >&2
    usage >&2
    exit 2
  fi
}

require_value "--release" "${RELEASE}"
require_value "--namespace" "${NAMESPACE}"

if [[ -n "${IMAGE}" && -n "${VERSION}" ]]; then
  echo "Error: pass only one of --image or --version" >&2
  exit 2
fi
if [[ -n "${IMAGE}" ]]; then
  doops_parse_image_digest "${IMAGE}"
elif [[ -n "${VERSION}" ]]; then
  doops_resolve_release_tag "${VERSION}"
else
  echo "Error: --image or --version is required" >&2
  usage >&2
  exit 2
fi

if [[ ! "${HANDOFF_TIMEOUT}" =~ ^[1-9][0-9]*$ ]]; then
  echo "Error: --handoff-timeout must be a positive integer" >&2
  exit 2
fi
if [[ ! -f "${CHART_DIR}/Chart.yaml" || ! -f "${CHART_DIR}/templates/deployment.yaml" ]]; then
  echo "Error: --chart-dir must be a doops-agent Helm chart" >&2
  exit 1
fi
chart_template="${CHART_DIR}/templates/deployment.yaml"
if ! grep -q '/app/sandbox-entrypoint.sh' "${chart_template}"; then
  echo "Error: candidate chart is missing sandbox-entrypoint.sh" >&2
  exit 1
fi
if ! grep -q 'maxUnavailable: 0' "${chart_template}" || ! grep -q 'maxSurge: 1' "${chart_template}"; then
  echo "Error: candidate chart must hand off with maxUnavailable 0 and maxSurge 1" >&2
  exit 1
fi
if grep -q 'hostNetwork:' "${chart_template}"; then
  echo "Error: candidate chart must not set hostNetwork" >&2
  exit 1
fi

echo "Release: ${RELEASE} / ${NAMESPACE}"
echo "Image: ${DOOPS_IMAGE}"

release_new_cluster() {
  require_value "--values" "${VALUES}"
  if [[ -n "${TARGET}" || -n "${SESSION}" ]]; then
    echo "Error: new-cluster --values must not be mixed with --target/--session" >&2
    exit 2
  fi
  if [[ "${PROXY_ALLOWED_HOSTS_SET}" == true ]]; then
    echo "Error: new-cluster proxy.allowedHosts must be configured in the values file" >&2
    exit 2
  fi
  if [[ "${HOST_ALIASES_SET}" == true ]]; then
    echo "Error: new-cluster hostAliases must be configured in the values file" >&2
    exit 2
  fi
  if [[ ! -f "${VALUES}" ]]; then
    echo "Error: --values file does not exist: ${VALUES}" >&2
    exit 1
  fi
  command -v helm >/dev/null || {
    echo "Error: helm is required on PATH" >&2
    exit 1
  }
  command -v kubectl >/dev/null || {
    echo "Error: kubectl is required on PATH" >&2
    exit 1
  }
  export KUBECONFIG="${KUBECONFIG_PATH:-${KUBECONFIG:-}}"
  helm_args=(
    upgrade --install "${RELEASE}" "${CHART_DIR}"
    --namespace "${NAMESPACE}"
    --create-namespace
    --values "${VALUES}"
    --set-string "image.repository=${DOOPS_IMAGE_REPOSITORY}"
    --set-string "image.digest=${DOOPS_IMAGE_DIGEST}"
    --timeout "${TIMEOUT}"
    --wait
  )
  echo "Values: ${VALUES}"
  if [[ "${DRY_RUN}" == true ]]; then
    helm "${helm_args[@]}" --dry-run=server
    echo "Dry-run complete. Helm was not mutated."
    exit 0
  fi
  helm "${helm_args[@]}"
  workload="$(helm get values "${RELEASE}" -n "${NAMESPACE}" -o json | python3 -c 'import json,sys; print(json.load(sys.stdin)["workload"]["name"])')"
  kubectl -n "${NAMESPACE}" rollout status "deployment/${workload}" --timeout "${TIMEOUT}"
  echo "Release complete: ${RELEASE} -> ${DOOPS_IMAGE}"
}

normalize_proxy_allowed_hosts() {
  python3 - "$1" <<'PY'
import ipaddress
import re
import sys

raw = sys.argv[1]
if raw == "":
    raise SystemExit(0)

label_pattern = re.compile(r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")
normalized = []
for item in raw.split(","):
    host = item.strip().lower()
    if not host:
        print("Error: --proxy-allowed-hosts contains an empty entry", file=sys.stderr)
        raise SystemExit(1)
    try:
        ipaddress.ip_address(host)
    except ValueError:
        labels = host.split(".")
        if (
            len(host) > 253
            or host.endswith(".")
            or any(not label_pattern.fullmatch(label) for label in labels)
        ):
            print(
                "Error: --proxy-allowed-hosts entries must be exact hostnames or IPs "
                "without scheme, port, path, wildcard, or trailing dot",
                file=sys.stderr,
            )
            raise SystemExit(1)
    if host not in normalized:
        normalized.append(host)

print(",".join(normalized), end="")
PY
}

normalize_host_aliases() {
  python3 - "$@" <<'PY'
import ipaddress
import json
import re
import sys

label_pattern = re.compile(r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")
aliases = {}
for raw in sys.argv[1:]:
    if raw.count("=") != 1:
        print("Error: --host-alias must use HOST=IP", file=sys.stderr)
        raise SystemExit(1)
    raw_host, raw_ip = raw.split("=", 1)
    host = raw_host.strip().lower()
    ip = raw_ip.strip()
    labels = host.split(".")
    if (
        not host
        or len(host) > 253
        or host.endswith(".")
        or any(not label_pattern.fullmatch(label) for label in labels)
    ):
        print(
            "Error: --host-alias HOST must be an exact hostname without "
            "scheme, port, path, wildcard, or trailing dot",
            file=sys.stderr,
        )
        raise SystemExit(1)
    try:
        normalized_ip = str(ipaddress.ip_address(ip))
    except ValueError:
        print("Error: --host-alias IP must be an IPv4 or IPv6 address", file=sys.stderr)
        raise SystemExit(1)
    previous = aliases.get(host)
    if previous is not None and previous != normalized_ip:
        print(
            f"Error: --host-alias assigns conflicting IPs to {host}",
            file=sys.stderr,
        )
        raise SystemExit(1)
    aliases[host] = normalized_ip

print(json.dumps(aliases, separators=(",", ":"), sort_keys=True), end="")
PY
}

release_existing_cluster() {
  require_value "--target" "${TARGET}"
  require_value "--session" "${SESSION}"
  if [[ -n "${VALUES}" ]]; then
    echo "Error: existing-cluster release reads live Helm values; do not pass --values" >&2
    exit 2
  fi
  if [[ "${PROXY_ALLOWED_HOSTS_SET}" == true ]]; then
    command -v python3 >/dev/null || {
      echo "Error: python3 is required to validate --proxy-allowed-hosts" >&2
      exit 1
    }
    if ! PROXY_ALLOWED_HOSTS="$(normalize_proxy_allowed_hosts "${PROXY_ALLOWED_HOSTS}")"; then
      exit 2
    fi
  fi
  if [[ "${HOST_ALIASES_SET}" == true ]]; then
    command -v python3 >/dev/null || {
      echo "Error: python3 is required to validate --host-alias" >&2
      exit 1
    }
    if ! HOST_ALIASES_JSON="$(normalize_host_aliases "${HOST_ALIAS_ARGS[@]}")"; then
      exit 2
    fi
  fi
  require_value "--cluster" "${CLUSTER}"
  require_value "--instance" "${INSTANCE}"
  require_value "--kubeconfig or KUBECONFIG" "${KUBECONFIG_PATH}"
  command -v doops >/dev/null || {
    echo "Error: doops CLI is required on PATH" >&2
    exit 1
  }
  command -v helm >/dev/null || {
    echo "Error: helm is required on PATH" >&2
    exit 1
  }
  command -v kubectl >/dev/null || {
    echo "Error: kubectl is required on PATH" >&2
    exit 1
  }
  if [[ ! -s "${KUBECONFIG_PATH}" ]]; then
    echo "Error: kubeconfig does not exist or is empty: ${KUBECONFIG_PATH}" >&2
    exit 1
  fi
  export KUBECONFIG="${KUBECONFIG_PATH}"

  doops_cmd() {
    doops -session "${SESSION}" "$@"
  }

  echo "Target: ${TARGET}"
  echo "Session: ${SESSION}"
  echo "Gateway identity: ${CLUSTER}/${INSTANCE}"
  echo "External kubeconfig: ${KUBECONFIG_PATH}"
  if [[ "${PROXY_ALLOWED_HOSTS_SET}" == true ]]; then
    echo "Proxy upstream hosts: ${PROXY_ALLOWED_HOSTS:-<deny-all>}"
  fi
  if [[ "${HOST_ALIASES_SET}" == true ]]; then
    echo "Host aliases: ${HOST_ALIASES_JSON}"
  fi
  echo "==> checking target is online"
  target_status="$(doops_cmd targets --target "${TARGET}")"
  target_identity="$(
    printf '%s\n' "${target_status}" |
      awk '$1 != "CLUSTER" && $1 !~ /^-+$/ && NF >= 2 {print $1 "/" $2; exit}'
  )"
  if [[ "${target_identity}" != "${CLUSTER}/${INSTANCE}" ]]; then
    echo "Error: Gateway target resolves to ${target_identity:-<unknown>}, not ${CLUSTER}/${INSTANCE}" >&2
    exit 1
  fi

  echo "==> external-controller preflight: Helm release, target identity, safe handoff"
  if ! helm status "${RELEASE}" -n "${NAMESPACE}" >/dev/null; then
    echo "Error: Helm release ${RELEASE} was not found in ${NAMESPACE}" >&2
    echo "Adopt the workload onto deploy/helm/doops-agent before releasing." >&2
    exit 1
  fi
  preflight="$(
    python3 - "${RELEASE}" "${NAMESPACE}" "${CLUSTER}" "${INSTANCE}" <<'PY'
import json, subprocess, sys
release, namespace, expected_cluster, expected_instance = sys.argv[1:5]
values = json.loads(subprocess.check_output(
    ["helm", "get", "values", release, "-n", namespace, "-o", "json"],
    text=True,
))
workload = ((values.get("workload") or {}).get("name") or "").strip()
if not workload:
    raise SystemExit("Error: Helm values are missing workload.name")
deploy = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "deploy", workload, "-o", "json"],
    text=True,
))
pod_spec = deploy["spec"]["template"]["spec"]
if pod_spec.get("hostNetwork"):
    raise SystemExit("Error: hostNetwork agents cannot hand off; refuse release")
container = pod_spec["containers"][0]
entry = " ".join(container.get("command") or []) + " " + " ".join(container.get("args") or [])
if "/app/sandbox-entrypoint.sh" not in entry:
    raise SystemExit(
        "Error: live command is not sandbox-entrypoint.sh; refusing to replace a custom entrypoint"
    )
env = {
    item.get("name"): item.get("value", "")
    for item in container.get("env") or []
}
if env.get("DOOPS_GATEWAY_CLUSTER") != expected_cluster:
    raise SystemExit("Error: kubeconfig workload cluster does not match --cluster")
if env.get("DOOPS_GATEWAY_INSTANCE") != expected_instance:
    raise SystemExit("Error: kubeconfig workload instance does not match --instance")
selector_labels = ((values.get("workload") or {}).get("selectorLabels") or {})
if not selector_labels:
    raise SystemExit("Error: Helm values are missing workload.selectorLabels")
selector = ",".join(f"{key}={selector_labels[key]}" for key in sorted(selector_labels))
print("workload=" + workload)
print("current_image=" + container.get("image", ""))
print("selector=" + selector)
PY
  )"
  echo "${preflight}"
  workload="$(printf '%s\n' "${preflight}" | awk -F= '$1 == "workload" {print substr($0, index($0, "=") + 1)}')"
  current_image="$(printf '%s\n' "${preflight}" | awk -F= '$1 == "current_image" {print substr($0, index($0, "=") + 1)}')"
  selector="$(printf '%s\n' "${preflight}" | awk -F= '$1 == "selector" {print substr($0, index($0, "=") + 1)}')"
  require_value "live workload.name" "${workload}"
  require_value "live workload.selectorLabels" "${selector}"

  release_tmp="$(mktemp -d)"
  cleanup_release_tmp() {
    rm -rf "${release_tmp}"
  }
  trap cleanup_release_tmp EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  release_values="${release_tmp}/values.json"
  helm get values "${RELEASE}" -n "${NAMESPACE}" -o json >"${release_values}"
  python3 - "${release_values}" "${DOOPS_IMAGE_REPOSITORY}" "${DOOPS_IMAGE_DIGEST}" "${PROXY_ALLOWED_HOSTS}" "${PROXY_ALLOWED_HOSTS_SET}" "${HOST_ALIASES_JSON}" "${HOST_ALIASES_SET}" <<'PY'
import json, sys
(
    path,
    repository,
    digest,
    proxy_allowed_hosts,
    proxy_override,
    host_aliases_json,
    host_aliases_override,
) = sys.argv[1:8]
with open(path, encoding="utf-8") as fh:
    values = json.load(fh)
image = values.setdefault("image", {})
image.pop("tag", None)
image["repository"] = repository
image["digest"] = digest
if proxy_override == "true":
    values.setdefault("proxy", {})["allowedHosts"] = proxy_allowed_hosts
if host_aliases_override == "true":
    overrides = json.loads(host_aliases_json)
    entries = []
    for item in values.get("hostAliases") or []:
        ip = str(item.get("ip") or "").strip()
        hostnames = [
            str(host).strip().lower()
            for host in item.get("hostnames") or []
            if str(host).strip().lower() not in overrides
        ]
        if ip and hostnames:
            entries.append({"ip": ip, "hostnames": hostnames})
    for host, ip in overrides.items():
        for item in entries:
            if item["ip"] == ip:
                item["hostnames"].append(host)
                break
        else:
            entries.append({"ip": ip, "hostnames": [host]})
    values["hostAliases"] = entries
with open(path, "w", encoding="utf-8") as fh:
    json.dump(values, fh)
PY
  helm_args=(
    upgrade --install "${RELEASE}" "${CHART_DIR}"
    -n "${NAMESPACE}"
    -f "${release_values}"
    --timeout "${TIMEOUT}"
    --wait=false
  )
  echo "==> server-side Helm dry-run from the external controller"
  helm "${helm_args[@]}" --dry-run=server >/dev/null
  if [[ "${DRY_RUN}" == true ]]; then
    trap - EXIT INT TERM
    cleanup_release_tmp
    echo "Dry-run complete. The target Agent did not execute a mutation."
    exit 0
  fi

  previous_revision="$(
    helm history "${RELEASE}" -n "${NAMESPACE}" -o json |
      python3 -c 'import json,sys; rows=json.load(sys.stdin); print(max(int(row["revision"]) for row in rows if row["status"] == "deployed"))'
  )"
  rollback_required=true

  rollback_release() {
    echo "==> rolling back ${RELEASE} to revision ${previous_revision}" >&2
    if ! helm rollback "${RELEASE}" "${previous_revision}" -n "${NAMESPACE}" \
      --timeout "${TIMEOUT}" --wait; then
      echo "Error: Helm rollback command failed" >&2
      return 1
    fi
    if ! kubectl -n "${NAMESPACE}" rollout status "deployment/${workload}" --timeout "${TIMEOUT}"; then
      echo "Error: rolled-back Deployment did not become ready" >&2
      return 1
    fi
    if ! doops -session "${SESSION}-rollback" targets --target "${TARGET}" >/dev/null; then
      echo "Error: rolled-back Agent did not reconnect to Gateway" >&2
      return 1
    fi
    if ! rolled_back_image="$(
      kubectl -n "${NAMESPACE}" get deployment "${workload}" \
        -o jsonpath='{.spec.template.spec.containers[0].image}'
    )"; then
      echo "Error: could not read the rolled-back image" >&2
      return 1
    fi
    if [[ "${rolled_back_image}" != "${current_image}" ]]; then
      echo "Error: rollback image mismatch: ${rolled_back_image}" >&2
      return 1
    fi
    echo "rollback_complete=true" >&2
  }

  release_exit() {
    code=$?
    trap - EXIT INT TERM
    if [[ "${rollback_required}" == true ]]; then
      if ! rollback_release; then
        echo "Error: automatic rollback failed; manual intervention is required" >&2
        code=1
      fi
    fi
    cleanup_release_tmp
    exit "${code}"
  }
  trap release_exit EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  echo "==> submitting Helm handoff from the external controller"
  helm "${helm_args[@]}"

  echo "==> waiting for handoff; live target must keep serving"
  deadline=$((SECONDS + HANDOFF_TIMEOUT))
  offline_streak=0
  candidate_failure_streak=0
  last_candidate_failure=""
  while true; do
    if (( SECONDS >= deadline )); then
      kubectl -n "${NAMESPACE}" get deployment,replicaset,pod -l "${selector}" -o wide >&2 || true
      kubectl -n "${NAMESPACE}" get events --sort-by=.lastTimestamp | tail -n 30 >&2 || true
      echo "Error: live image did not become ${DOOPS_IMAGE} within ${HANDOFF_TIMEOUT}s" >&2
      exit 1
    fi
    kubectl -n "${NAMESPACE}" get deployment "${workload}" -o json >"${release_tmp}/deployment.json"
    kubectl -n "${NAMESPACE}" get pods -l "${selector}" -o json >"${release_tmp}/pods.json"
    candidate_failure="$(
      python3 - "${release_tmp}/deployment.json" "${release_tmp}/pods.json" "${DOOPS_IMAGE}" <<'PY'
import json
import sys

deployment_path, pods_path, expected_image = sys.argv[1:4]
with open(deployment_path, encoding="utf-8") as fh:
    deployment = json.load(fh)
with open(pods_path, encoding="utf-8") as fh:
    pods = json.load(fh)

for condition in deployment.get("status", {}).get("conditions") or []:
    if (
        condition.get("type") == "Progressing"
        and condition.get("status") == "False"
    ):
        print(
            "deployment:"
            + str(condition.get("reason") or "ProgressingFalse")
            + ":"
            + str(condition.get("message") or "")
        )
        raise SystemExit(0)

fatal_waiting = {
    "CrashLoopBackOff",
    "CreateContainerConfigError",
    "CreateContainerError",
    "ErrImageNeverPull",
    "ErrImagePull",
    "ImagePullBackOff",
    "InvalidImageName",
    "RunContainerError",
}
for pod in pods.get("items") or []:
    containers = pod.get("spec", {}).get("containers") or []
    if not containers or containers[0].get("image") != expected_image:
        continue
    name = pod.get("metadata", {}).get("name", "candidate")
    for condition in pod.get("status", {}).get("conditions") or []:
        if (
            condition.get("type") == "PodScheduled"
            and condition.get("status") == "False"
            and condition.get("reason") == "Unschedulable"
        ):
            print("pod:" + name + ":Unschedulable:" + str(condition.get("message") or ""))
            raise SystemExit(0)
    for status in pod.get("status", {}).get("containerStatuses") or []:
        waiting = (status.get("state") or {}).get("waiting") or {}
        reason = str(waiting.get("reason") or "")
        if reason in fatal_waiting:
            print("pod:" + name + ":" + reason + ":" + str(waiting.get("message") or ""))
            raise SystemExit(0)
PY
    )"
    if [[ -n "${candidate_failure}" ]]; then
      if [[ "${candidate_failure}" == "${last_candidate_failure}" ]]; then
        candidate_failure_streak=$((candidate_failure_streak + 1))
      else
        candidate_failure_streak=1
        last_candidate_failure="${candidate_failure}"
      fi
      echo "candidate_failure_observation=${candidate_failure_streak}: ${candidate_failure}" >&2
      if (( candidate_failure_streak >= 3 )); then
        kubectl -n "${NAMESPACE}" get pods -l "${selector}" -o wide >&2 || true
        kubectl -n "${NAMESPACE}" get events --sort-by=.lastTimestamp | tail -n 30 >&2 || true
        exit 1
      fi
    else
      candidate_failure_streak=0
      last_candidate_failure=""
    fi
    if ! doops -session "${SESSION}-wait" targets --target "${TARGET}" >/dev/null 2>&1; then
      offline_streak=$((offline_streak + 5))
      if (( offline_streak >= 30 )); then
        echo "Error: target ${TARGET} went offline for 30s during handoff" >&2
        exit 1
      fi
      sleep 5
      continue
    fi
    offline_streak=0
    if out="$(doops -session "${SESSION}-wait" exec --target "${TARGET}" --cmd "$(cat <<EOF
python3 - $(printf '%q' "${NAMESPACE}") $(printf '%q' "${DOOPS_IMAGE}") <<'PY'
import json, os, subprocess, sys
namespace, expected = sys.argv[1], sys.argv[2]
pod = os.environ.get("HOSTNAME", "")
if not pod:
    raise SystemExit(2)
data = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "pod", pod, "-o", "json"],
    text=True,
))
ready = False
for cond in data.get("status", {}).get("conditions") or []:
    if cond.get("type") == "Ready" and cond.get("status") == "True":
        ready = True
got = data["spec"]["containers"][0]["image"]
print("pod=" + pod)
print("pod_image=" + got)
print("ready=" + str(ready).lower())
if not ready or got != expected:
    raise SystemExit(2)
PY
EOF
)" 2>/dev/null)"; then
      echo "${out}" | grep -v TARGETING || true
      break
    fi
    sleep 5
  done

  echo "==> verifying replacement binary"
  doops -session "${SESSION}-verify" exec --target "${TARGET}" --cmd "$(cat <<EOF
set -eu
/app/doops-agent -h >/tmp/doops-agent-help.txt 2>&1 || true
if ! grep -q -- '-edge' /tmp/doops-agent-help.txt; then
  echo "Error: released binary is missing -edge flags" >&2
  exit 1
fi
echo "edge_flag=documented"
hostname
sha256sum /app/doops-agent
EOF
)"
  doops -session "${SESSION}-verify" exec --target "${TARGET}" --cmd "$(cat <<EOF
set -eu
python3 - $(printf '%q' "${RELEASE}") $(printf '%q' "${NAMESPACE}") $(printf '%q' "${HOST_ALIASES_JSON}") $(printf '%q' "${HOST_ALIASES_SET}") <<'PY'
import json
import os
import socket
import subprocess
import sys

release, namespace, host_aliases_json, host_aliases_override = sys.argv[1:5]
values = json.loads(subprocess.check_output(
    ["helm", "get", "values", release, "-n", namespace, "-o", "json"],
    text=True,
))
expected = str((values.get("proxy") or {}).get("allowedHosts") or "")
pod = os.environ.get("HOSTNAME", "")
if not pod:
    raise SystemExit("Error: replacement Agent HOSTNAME is empty")
pod_data = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "pod", pod, "-o", "json"],
    text=True,
))
container_env = {
    item.get("name"): item.get("value", "")
    for item in pod_data["spec"]["containers"][0].get("env") or []
}
actual = container_env.get("DOOPS_PROXY_ALLOW_HOSTS", "")
print("proxy_allowed_hosts=" + actual)
if actual != expected:
    raise SystemExit(
        "Error: replacement Agent proxy allowlist does not match live Helm values"
    )

def normalized_aliases(items):
    result = {}
    for item in items or []:
        ip = str(item.get("ip") or "").strip()
        for host in item.get("hostnames") or []:
            result[str(host).strip().lower()] = ip
    return result

expected_aliases = normalized_aliases(values.get("hostAliases"))
actual_aliases = normalized_aliases(
    pod_data.get("spec", {}).get("hostAliases")
)
print("host_aliases=" + json.dumps(actual_aliases, sort_keys=True))
if actual_aliases != expected_aliases:
    raise SystemExit(
        "Error: replacement Agent host aliases do not match live Helm values"
    )
if host_aliases_override == "true":
    requested_aliases = json.loads(host_aliases_json)
    for host, expected_ip in requested_aliases.items():
        resolved = sorted({
            item[4][0]
            for item in socket.getaddrinfo(host, None)
        })
        print(f"host_alias_resolution={host}:{','.join(resolved)}")
        if expected_ip not in resolved:
            raise SystemExit(
                f"Error: replacement Agent did not resolve {host} to {expected_ip}"
            )
PY
EOF
)"
  kubectl -n "${NAMESPACE}" rollout status "deployment/${workload}" --timeout "${TIMEOUT}"
  kubectl -n "${NAMESPACE}" get pods -l "${selector}" -o json >"${release_tmp}/verified-pods.json"
  python3 - "${release_tmp}/verified-pods.json" "${DOOPS_IMAGE}" "${DOOPS_IMAGE_DIGEST}" <<'PY'
import json
import sys

pods_path, expected_image, expected_digest = sys.argv[1:4]
with open(pods_path, encoding="utf-8") as fh:
    pods = json.load(fh)
matches = []
for pod in pods.get("items") or []:
    containers = pod.get("spec", {}).get("containers") or []
    if not containers or containers[0].get("image") != expected_image:
        continue
    statuses = pod.get("status", {}).get("containerStatuses") or []
    ready = any(
        condition.get("type") == "Ready" and condition.get("status") == "True"
        for condition in pod.get("status", {}).get("conditions") or []
    )
    if not ready or not statuses:
        continue
    status = statuses[0]
    if int(status.get("restartCount") or 0) != 0:
        raise SystemExit("Error: replacement Agent restarted before release completion")
    image_id = str(status.get("imageID") or "")
    if expected_digest not in image_id:
        raise SystemExit("Error: replacement Agent imageID does not match requested digest")
    matches.append((pod.get("metadata", {}).get("name", ""), image_id))
if len(matches) != 1:
    raise SystemExit("Error: expected exactly one Ready replacement Agent Pod")
print("replacement_pod=" + matches[0][0])
print("replacement_image_id=" + matches[0][1])
PY
  rollback_required=false
  trap - EXIT INT TERM
  cleanup_release_tmp
  echo "Release complete: ${TARGET} -> ${DOOPS_IMAGE}"
}

if [[ -n "${VALUES}" ]]; then
  release_new_cluster
else
  release_existing_cluster
fi
