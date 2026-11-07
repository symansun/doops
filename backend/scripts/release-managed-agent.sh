#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Install into a new cluster or externally reconcile an existing Helm-managed
doops-agent. Use this script for a new cluster without an Agent, a Helm-managed
Agent without the in-cluster self-upgrade controller, or explicit Helm runtime
configuration changes. Managed Agents with the controller use `doops upgrade`
for image-only changes without an operator kubeconfig. A workload without a Helm
release is adopted first (see docs/BUILD_DEPLOY_SOP.md); this script then manages it.

Helm chart deploy/helm/doops-agent starts the replacement Pod first
(maxSurge=1, maxUnavailable=0). The live Agent keeps serving until the new Pod
is Ready and has replaced the Gateway session.

Existing Helm release (DoOps target already online):

  bash scripts/release-managed-agent.sh \
    --target gw-edu-coder \
    --session agent-release-<commit> \
    --cluster doops-edu \
    --instance edu-coder \
    --release doops-agent-live \
    --namespace kz-ops \
    --kubeconfig /secure/path/to/kubeconfig \
    --proxy-allowed-hosts 127.0.0.1,internal.example.com \
    --host-alias internal.example.com=10.0.0.10 \
    --image ghcr.io/symansun/doops@sha256:<verified-ci-digest>

New cluster (operator kubeconfig, complete values, no Agent yet):

  bash scripts/release-managed-agent.sh \
    --release doops-agent \
    --namespace doops-system \
    --values /path/to/complete-values.yaml \
    --kubeconfig /secure/path/to/kubeconfig \
    --image ghcr.io/symansun/doops@sha256:<verified-ci-digest>

Required:
  --release NAME
  --namespace NAME
  --image REPO@sha256:<digest>   or  --version vX.Y.Z

The publisher must verify the exact CI commit and image digest before release.
--version resolves a tag to a digest; it does not verify CI provenance.

Existing cluster also requires:
  --target NAME
  --session NAME
  --cluster NAME
  --instance NAME
  --kubeconfig PATH (or KUBECONFIG)

New cluster also requires:
  --values FILE
  --kubeconfig PATH (or KUBECONFIG)

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
if grep -Eq '^[[:space:]]+hostNetwork:[[:space:]]+true([[:space:]]*)$' "${chart_template}"; then
  echo "Error: candidate chart must disable hostNetwork" >&2
  exit 1
fi

echo "Release: ${RELEASE} / ${NAMESPACE}"
echo "Image: ${DOOPS_IMAGE}"

verify_final_deployment_state() {
  local workload_name="$1"
  local expected_image="$2"
  local deployment_json
  deployment_json="$(mktemp "${TMPDIR:-/tmp}/doops-final-deployment.XXXXXX")"
  if ! kubectl -n "${NAMESPACE}" get deployment "${workload_name}" -o json >"${deployment_json}"; then
    rm -f "${deployment_json}"
    return 1
  fi
  local status
  if python3 - "${deployment_json}" "${workload_name}" "${expected_image}" <<'PY'
import json
import sys

deployment_path, expected_workload, expected_image = sys.argv[1:4]
with open(deployment_path, encoding="utf-8") as fh:
    deployment = json.load(fh)

metadata = deployment.get("metadata") or {}
spec = deployment.get("spec") or {}
status = deployment.get("status") or {}
name = str(metadata.get("name") or "")
if name != expected_workload:
    raise SystemExit(
        f"Error: final Deployment identity changed: {name or '<unknown>'}"
    )

generation = int(metadata.get("generation") or 0)
observed_generation = int(status.get("observedGeneration") or 0)
if generation <= 0 or observed_generation != generation:
    raise SystemExit(
        "Error: final Deployment controller has not observed the current generation"
    )

desired = int(spec.get("replicas") if spec.get("replicas") is not None else 1)
for field in ("updatedReplicas", "readyReplicas", "availableReplicas"):
    actual = int(status.get(field) or 0)
    if actual != desired:
        raise SystemExit(
            f"Error: final Deployment {field}={actual}, desiredReplicas={desired}"
        )
if int(status.get("unavailableReplicas") or 0) != 0:
    raise SystemExit("Error: final Deployment still has unavailable replicas")

containers = ((spec.get("template") or {}).get("spec") or {}).get("containers") or []
if not containers or containers[0].get("image") != expected_image:
    actual = containers[0].get("image") if containers else "<none>"
    raise SystemExit(
        f"Error: final Deployment image mismatch: {actual} != {expected_image}"
    )

print(
    "final_deployment="
    + name
    + " generation="
    + str(generation)
    + " image="
    + expected_image
    + " ready="
    + str(desired)
)
PY
  then
    status=0
  else
    status=$?
  fi
  rm -f "${deployment_json}"
  return "${status}"
}

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
  require_value "--kubeconfig (or KUBECONFIG)" "${KUBECONFIG_PATH}"
  if [[ ! -s "${KUBECONFIG_PATH}" ]]; then
    echo "Error: bootstrap operator kubeconfig does not exist or is empty: ${KUBECONFIG_PATH}" >&2
    exit 1
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
  export KUBECONFIG="${KUBECONFIG_PATH}"
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
  verify_final_deployment_state "${workload}" "${DOOPS_IMAGE}"
  kubectl -n "${NAMESPACE}" rollout status "deployment/${workload}" --timeout "${TIMEOUT}"
  echo "Bootstrap resources ready: ${RELEASE} -> ${DOOPS_IMAGE}"
  echo "Gateway handoff and Agent capabilities have not been verified; complete docs/CLUSTER_INSTALL_GUIDE.md acceptance before declaring the target ready."
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
  if [[ -z "${KUBECONFIG_PATH}" ]]; then
    cat >&2 <<'EOF'
Error: this is the external Helm controller path, not a normal workload deployment.
An external operator kubeconfig is required for this script only.
For a Helm-managed Agent with the self-upgrade controller, use:
  doops -session <session> upgrade --target <target> --image <repository>@sha256:<digest>
That image-only path runs an in-cluster Job and does not require a local kubeconfig.
Use this external script for an Agent without that controller or explicit Helm runtime configuration changes.
For ordinary workload releases, use:
  doops -session <session> ask --target <target> --apply --msg '<target、服务范围、镜像版本、原因和验收条件>'
EOF
    exit 2
  fi
  command -v doops >/dev/null || {
    echo "Error: doops CLI is required on PATH" >&2
    exit 1
  }
  if ! doops capabilities --help >/dev/null 2>&1; then
    echo "Error: installed doops CLI lacks the capabilities protocol probe; install the current CLI release before mutating an Agent" >&2
    exit 1
  fi
  cli_release_commit="$(
    doops version 2>/dev/null |
      awk '{for (i=1; i<=NF; i++) if ($i ~ /^commit=/) {sub(/^commit=/, "", $i); print $i}}'
  )"
  if [[ ! "${cli_release_commit}" =~ ^[0-9a-f]{40}$ ]]; then
    echo "Error: installed doops CLI does not expose a full release commit; install the current CLI release before mutating an Agent" >&2
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
  if [[ ! -s "${KUBECONFIG_PATH}" ]]; then
    echo "Error: external operator kubeconfig does not exist or is empty: ${KUBECONFIG_PATH}" >&2
    echo "This external script requires a kubeconfig; managed image-only upgrades use doops upgrade, and ordinary workload releases use doops ask --apply." >&2
    exit 1
  fi
  export KUBECONFIG="${KUBECONFIG_PATH}"

  echo "==> checking Kubernetes API reachability"
  api_check_output=""
  if ! api_check_output="$(kubectl version --request-timeout=10s 2>&1)"; then
    echo "Error: Kubernetes API is unreachable or rejected the kubeconfig; Helm release state was not checked." >&2
    printf '%s\n' "${api_check_output}" >&2
    exit 1
  fi

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
  helm_status_output=""
  if ! helm_status_output="$(helm status "${RELEASE}" -n "${NAMESPACE}" 2>&1)"; then
    echo "Error: Helm release ${RELEASE} was not found or is not readable in ${NAMESPACE}." >&2
    printf '%s\n' "${helm_status_output}" >&2
    echo "Adopt the workload onto deploy/helm/doops-agent before releasing only when the Kubernetes API is reachable and the release is genuinely absent." >&2
    exit 1
  fi
  preflight="$(
    python3 - "${RELEASE}" "${NAMESPACE}" "${CLUSTER}" "${INSTANCE}" <<'PY'
import json, subprocess, sys
release, namespace, expected_cluster, expected_instance = sys.argv[1:5]
values = json.loads(subprocess.check_output(
    ["helm", "get", "values", release, "-n", namespace, "-o", "json"],
    universal_newlines=True,
))
workload = ((values.get("workload") or {}).get("name") or "").strip()
if not workload:
    raise SystemExit("Error: Helm values are missing workload.name")
deploy = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "deploy", workload, "-o", "json"],
    universal_newlines=True,
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
live_selector_labels = (
    (deploy.get("spec") or {}).get("selector", {}).get("matchLabels") or {}
)
if not live_selector_labels:
    raise SystemExit("Error: live Deployment is missing spec.selector.matchLabels")
template_labels = ((deploy.get("spec") or {}).get("template") or {}).get(
    "metadata", {}
).get("labels") or {}
for key, value in live_selector_labels.items():
    if str(template_labels.get(key)) != str(value):
        raise SystemExit(
            "Error: live Deployment selector does not match template labels"
        )
selector = ",".join(
    f"{key}={live_selector_labels[key]}" for key in sorted(live_selector_labels)
)
print("workload=" + workload)
print("current_image=" + container.get("image", ""))
print("selector=" + selector)
print(
    "selector_json="
    + json.dumps(live_selector_labels, separators=(",", ":"), sort_keys=True)
)
PY
  )"
  echo "${preflight}"
  workload="$(printf '%s\n' "${preflight}" | awk -F= '$1 == "workload" {print substr($0, index($0, "=") + 1)}')"
  current_image="$(printf '%s\n' "${preflight}" | awk -F= '$1 == "current_image" {print substr($0, index($0, "=") + 1)}')"
  selector="$(printf '%s\n' "${preflight}" | awk -F= '$1 == "selector" {print substr($0, index($0, "=") + 1)}')"
  selector_json="$(printf '%s\n' "${preflight}" | awk -F= '$1 == "selector_json" {print substr($0, index($0, "=") + 1)}')"
  require_value "live workload.name" "${workload}"
  require_value "live Deployment selector" "${selector}"
  require_value "live Deployment selector JSON" "${selector_json}"

  release_tmp="$(mktemp -d)"
  cleanup_release_tmp() {
    rm -rf "${release_tmp}"
  }
  trap cleanup_release_tmp EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  release_values="${release_tmp}/values.json"
  pre_release_pods="${release_tmp}/pre-release-pods.json"
  kubectl -n "${NAMESPACE}" get pods -l "${selector}" -o json >"${pre_release_pods}"
  helm get values "${RELEASE}" -n "${NAMESPACE}" -o json >"${release_values}"
  python3 - "${release_values}" "${DOOPS_IMAGE_REPOSITORY}" "${DOOPS_IMAGE_DIGEST}" "${PROXY_ALLOWED_HOSTS}" "${PROXY_ALLOWED_HOSTS_SET}" "${HOST_ALIASES_JSON}" "${HOST_ALIASES_SET}" "${selector_json}" <<'PY'
import json, sys
(
    path,
    repository,
    digest,
    proxy_allowed_hosts,
    proxy_override,
    host_aliases_json,
    host_aliases_override,
    selector_json,
) = sys.argv[1:9]
with open(path, encoding="utf-8") as fh:
    values = json.load(fh)
image = values.setdefault("image", {})
image.pop("tag", None)
image["repository"] = repository
image["digest"] = digest
values.setdefault("workload", {})["selectorLabels"] = json.loads(selector_json)
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
    if ! verify_final_deployment_state "${workload}" "${current_image}"; then
      echo "Error: rolled-back Deployment final state is not verified" >&2
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
    current_pods="${release_tmp}/current-pods.json"
    kubectl -n "${NAMESPACE}" get pods -l "${selector}" -o json >"${current_pods}"
    candidate_pod="$(
      python3 - "${pre_release_pods}" "${current_pods}" "${DOOPS_IMAGE}" "${DOOPS_IMAGE_DIGEST}" <<'PY'
import json
import sys

previous_path, current_path, expected_image, expected_digest = sys.argv[1:5]
with open(previous_path, encoding="utf-8") as fh:
    previous = {
        item.get("metadata", {}).get("name")
        for item in (json.load(fh).get("items") or [])
    }
with open(current_path, encoding="utf-8") as fh:
    current = json.load(fh)
matches = []
for pod in current.get("items") or []:
    name = pod.get("metadata", {}).get("name", "")
    if not name or name in previous:
        continue
    containers = pod.get("spec", {}).get("containers") or []
    if not containers or containers[0].get("image") != expected_image:
        continue
    ready = any(
        condition.get("type") == "Ready" and condition.get("status") == "True"
        for condition in pod.get("status", {}).get("conditions") or []
    )
    statuses = pod.get("status", {}).get("containerStatuses") or []
    if not ready or len(statuses) != 1:
        continue
    if int(statuses[0].get("restartCount") or 0) != 0:
        continue
    if expected_digest not in str(statuses[0].get("imageID") or ""):
        continue
    matches.append(name)
if len(matches) != 1:
    raise SystemExit(1)
print(matches[0])
PY
    )" || candidate_pod=""
    if [[ -n "${candidate_pod}" ]] &&
      doops -session "${SESSION}-wait" targets --target "${TARGET}" >/dev/null 2>&1; then
      handoff_host="$(
        doops -session "${SESSION}-wait" exec --target "${TARGET}" \
          --cmd hostname 2>/dev/null || true
      )"
      if printf '%s\n' "${handoff_host}" | grep -Fqx "${candidate_pod}"; then
        echo "pod=${candidate_pod}"
        echo "pod_image=${DOOPS_IMAGE}"
        echo "ready=true"
        replacement_pod="${candidate_pod}"
        break
      fi
    fi
    sleep 5
  done

  echo "==> verifying structured SSH protocol capability"
  if ! doops -session "${SESSION}-capabilities" capabilities \
    --target "${TARGET}" --require structuredSSH; then
    echo "Error: replacement Agent did not advertise structuredSSH; refusing release completion" >&2
    exit 1
  fi
  replacement_status="$(doops_cmd targets --target "${TARGET}")"
  replacement_release_commit="$(
    printf '%s\n' "${replacement_status}" |
      awk '$1 != "CLUSTER" && $1 !~ /^-+$/ && NF >= 7 {print $7; exit}'
  )"
  if [[ "${replacement_release_commit}" != "${cli_release_commit}" ]]; then
    echo "Error: replacement Agent release commit mismatch: got ${replacement_release_commit:-<unknown>}, want ${cli_release_commit}" >&2
    exit 1
  fi
  echo "release_commit=${replacement_release_commit}"

  echo "==> verifying replacement binary"
  replacement_help="$(
    kubectl -n "${NAMESPACE}" exec "${replacement_pod}" -- /app/doops-agent -h 2>&1 || true
  )"
  if ! printf '%s\n' "${replacement_help}" | grep -q -- '-edge'; then
    echo "Error: released binary is missing -edge flags" >&2
    exit 1
  fi
  echo "edge_flag=documented"
  kubectl -n "${NAMESPACE}" exec "${replacement_pod}" -- sha256sum /app/doops-agent
  python3 - "${RELEASE}" "${NAMESPACE}" "${replacement_pod}" <<'PY'
import json
import subprocess
import sys

release, namespace, pod = sys.argv[1:4]
values = json.loads(subprocess.check_output(
    ["helm", "get", "values", release, "-n", namespace, "-o", "json"],
    universal_newlines=True,
))
expected = str((values.get("proxy") or {}).get("allowedHosts") or "")
pod_data = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "pod", pod, "-o", "json"],
    universal_newlines=True,
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
PY
  requested_aliases="$(
    python3 - "${HOST_ALIASES_JSON}" "${HOST_ALIASES_SET}" <<'PY'
import json
import sys

aliases, override = sys.argv[1:3]
if override == "true":
    for host, ip in json.loads(aliases).items():
        print(f"{host}={ip}")
PY
  )"
  while IFS='=' read -r host expected_ip; do
    [[ -n "${host}" ]] || continue
    resolved="$(
      kubectl -n "${NAMESPACE}" exec "${replacement_pod}" -- \
        python3 -c 'import socket,sys; print(",".join(sorted({item[4][0] for item in socket.getaddrinfo(sys.argv[1], None)})))' \
        "${host}"
    )"
    echo "host_alias_resolution=${host}:${resolved}"
    if ! printf '%s\n' "${resolved}" | tr ',' '\n' | grep -Fqx "${expected_ip}"; then
      echo "Error: replacement Agent did not resolve ${host} to ${expected_ip}" >&2
      exit 1
    fi
  done <<<"${requested_aliases}"
  kubectl -n "${NAMESPACE}" rollout status "deployment/${workload}" --timeout "${TIMEOUT}"
  verify_final_deployment_state "${workload}" "${DOOPS_IMAGE}"
  kubectl -n "${NAMESPACE}" get pod "${replacement_pod}" -o json >"${release_tmp}/verified-pod.json"
  python3 - "${release_tmp}/verified-pod.json" "${replacement_pod}" "${DOOPS_IMAGE}" "${DOOPS_IMAGE_DIGEST}" <<'PY'
import json
import sys

pod_path, expected_pod, expected_image, expected_digest = sys.argv[1:5]
with open(pod_path, encoding="utf-8") as fh:
    pod = json.load(fh)
if pod.get("metadata", {}).get("name") != expected_pod:
    raise SystemExit("Error: replacement Agent Pod identity changed during verification")
containers = pod.get("spec", {}).get("containers") or []
statuses = pod.get("status", {}).get("containerStatuses") or []
ready = any(
    condition.get("type") == "Ready" and condition.get("status") == "True"
    for condition in pod.get("status", {}).get("conditions") or []
)
if not containers or containers[0].get("image") != expected_image:
    raise SystemExit("Error: replacement Agent image changed during verification")
if not ready or len(statuses) != 1:
    raise SystemExit("Error: replacement Agent is not Ready during verification")
status = statuses[0]
if int(status.get("restartCount") or 0) != 0:
    raise SystemExit("Error: replacement Agent restarted before release completion")
image_id = str(status.get("imageID") or "")
if expected_digest not in image_id:
    raise SystemExit("Error: replacement Agent imageID does not match requested digest")
print("replacement_pod=" + expected_pod)
print("replacement_image_id=" + image_id)
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
