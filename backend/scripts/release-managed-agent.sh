#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Upgrade an online Kubernetes Agent through the authorized, audited Gateway.
The installed doops CLI owns preflight, the independent controller Job, backup,
verification and automatic rollback. Its release commit must match the candidate
Agent image. Verify CI provenance and the pinned digest before invoking this script.

From backend/:
  bash scripts/release-managed-agent.sh \
    --target <configured-target> \
    --session agent-release-<commit> \
    --release <helm-release> \
    --namespace <agent-namespace> \
    --image <repository>@sha256:<verified-ci-digest>

Required: --target, --session, --release, --namespace, --image
Options:
  --dry-run   Gateway parameter preview only; no live preflight or Job is run.
  -h, --help

The local Helm/kubectl controller and new-cluster --values installation are retired.
--kubeconfig, --values, --chart-dir, --cluster, --instance, --timeout,
--handoff-timeout, --proxy-allowed-hosts, --host-alias and --version are rejected.
KUBECONFIG is not read or forwarded. This script never resolves images with Docker.

An old Agent without the upgrade controller needs an independently reviewed Job
submitted through `doops -session <s> exec --target <t>`. That Job must preserve
backup, Ready/digest/Gateway/release_commit verification and automatic rollback.
This script does not implement that first-upgrade path. Never replace the Agent
from its own process. An offline Gateway, unavailable audit or unsupported Agent
is a failure, with no local mutation or alternate controller.
EOF
}

fail() {
  printf 'Error: %s\n' "$1" >&2
  exit 2
}

TARGET=""
SESSION=""
RELEASE=""
NAMESPACE=""
IMAGE=""
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target|--session|--release|--namespace|--image)
      if [[ $# -lt 2 || -z "${2:-}" || "${2:-}" == -* ]]; then
        fail "$1 requires a value"
      fi
      case "$1" in
        --target) TARGET="$2" ;;
        --session) SESSION="$2" ;;
        --release) RELEASE="$2" ;;
        --namespace) NAMESPACE="$2" ;;
        --image) IMAGE="$2" ;;
      esac
      shift 2
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    --kubeconfig|--kubeconfig=*|--values|--values=*|--chart-dir|--chart-dir=*|\
    --cluster|--cluster=*|--instance|--instance=*|--timeout|--timeout=*|\
    --handoff-timeout|--handoff-timeout=*|--proxy-allowed-hosts|--proxy-allowed-hosts=*|\
    --host-alias|--host-alias=*|--version|--version=*)
      fail "${1%%=*} is retired; only an audited Gateway upgrade is supported. Use --target, --session, --release, --namespace and a verified --image digest; see docs/BUILD_DEPLOY_SOP.md for first-upgrade limits."
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unsupported argument; see --help for the Gateway-only interface"
      ;;
  esac
done

[[ -n "${TARGET//[[:space:]]/}" ]] || fail "--target is required"
[[ -n "${SESSION//[[:space:]]/}" ]] || fail "--session is required"
[[ -n "${RELEASE//[[:space:]]/}" ]] || fail "--release is required"
[[ -n "${NAMESPACE//[[:space:]]/}" ]] || fail "--namespace is required"
[[ -n "${IMAGE}" ]] || fail "--image is required"
if [[ ! "${IMAGE}" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]]; then
  fail "--image must be repository@sha256:<64-hex>"
fi

command -v doops >/dev/null 2>&1 || {
  printf '%s\n' "Error: install the published doops CLI matching the candidate Agent commit" >&2
  exit 1
}

upgrade_args=(
  -session "${SESSION}" upgrade
  --target "${TARGET}"
  --mode k8s
  --release "${RELEASE}"
  --namespace "${NAMESPACE}"
  --image "${IMAGE}"
)
if [[ "${DRY_RUN}" == true ]]; then
  upgrade_args+=(--dry-run)
fi

# Keep the existing CLI/Job transaction in charge, including failure and signals.
unset KUBECONFIG
exec doops "${upgrade_args[@]}"
