#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Bootstrap an isolated release-runner from an existing doops-agent Helm release.

Usage:
  bash scripts/bootstrap-release-runner.sh \
    --namespace ai \
    --managed-release doops-agent \
    --runner-release doops-release-runner \
    --runner-workload doops-release-runner \
    --runner-instance release-runner \
    --runner-home /var/lib/doops-release-runner-home \
    --image repository@sha256:<digest>
EOF
}

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NAMESPACE=""
MANAGED_RELEASE=""
RUNNER_RELEASE=""
RUNNER_WORKLOAD=""
RUNNER_INSTANCE=""
RUNNER_HOME=""
IMAGE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace)
      NAMESPACE="${2:-}"
      shift 2
      ;;
    --managed-release)
      MANAGED_RELEASE="${2:-}"
      shift 2
      ;;
    --runner-release)
      RUNNER_RELEASE="${2:-}"
      shift 2
      ;;
    --runner-workload)
      RUNNER_WORKLOAD="${2:-}"
      shift 2
      ;;
    --runner-instance)
      RUNNER_INSTANCE="${2:-}"
      shift 2
      ;;
    --runner-home)
      RUNNER_HOME="${2:-}"
      shift 2
      ;;
    --image)
      IMAGE="${2:-}"
      shift 2
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

dns_name='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
for value in "${NAMESPACE}" "${MANAGED_RELEASE}" "${RUNNER_RELEASE}" "${RUNNER_WORKLOAD}" "${RUNNER_INSTANCE}"; do
  if [[ ! "${value}" =~ ${dns_name} ]]; then
    echo "Invalid Kubernetes name: ${value}" >&2
    exit 2
  fi
done
if [[ ! "${RUNNER_HOME}" =~ ^/[A-Za-z0-9._/-]+$ ]] ||
  [[ "/${RUNNER_HOME#/}/" == *"/../"* ]]; then
  echo "Invalid runner home path" >&2
  exit 2
fi
if [[ ! "${IMAGE}" =~ ^([^[:space:]@]+)@(sha256:[a-f0-9]{64})$ ]]; then
  echo "Image must use an immutable repository@sha256 digest" >&2
  exit 2
fi
if [[ "${RUNNER_RELEASE}" == "${MANAGED_RELEASE}" ]]; then
  echo "Runner release must differ from the managed release" >&2
  exit 2
fi

IMAGE_REPOSITORY="${BASH_REMATCH[1]}"
IMAGE_DIGEST="${BASH_REMATCH[2]}"
VALUES_FILE="$(mktemp)"
trap 'rm -f "${VALUES_FILE}"' EXIT

EXISTING_RUNNER_RELEASES="$(
  helm list \
    --namespace "${NAMESPACE}" \
    --all \
    --short \
    --filter "^${RUNNER_RELEASE}$"
)"
if [[ -n "${EXISTING_RUNNER_RELEASES}" ]]; then
  echo "Runner release already exists: ${NAMESPACE}/${RUNNER_RELEASE}" >&2
  exit 2
fi

helm get values "${MANAGED_RELEASE}" \
  --namespace "${NAMESPACE}" \
  --output yaml >"${VALUES_FILE}"

MANAGED_WORKLOAD="$(
python3 - "${VALUES_FILE}" "${RUNNER_WORKLOAD}" "${RUNNER_INSTANCE}" "${RUNNER_HOME}" "${IMAGE_REPOSITORY}" "${IMAGE_DIGEST}" <<'PY'
import sys

import yaml

path, workload, instance, runner_home, repository, digest = sys.argv[1:]
with open(path, encoding="utf-8") as source:
    values = yaml.safe_load(source)

managed_workload = values["workload"]["name"]
if managed_workload == workload:
    raise SystemExit("runner workload must differ from the managed workload")
if values["gateway"]["instance"] == instance:
    raise SystemExit("runner instance must differ from the managed instance")

values["workload"]["name"] = workload
values["workload"]["selectorLabels"] = {"app": workload}
values["gateway"]["instance"] = instance
values["image"]["repository"] = repository
values["image"]["digest"] = digest

agent_home = next(
    (volume for volume in values.get("volumes", []) if volume.get("name") == "agent-home"),
    None,
)
if agent_home is None or "hostPath" not in agent_home:
    raise SystemExit("current release does not define an agent-home hostPath volume")
if agent_home["hostPath"].get("path") == runner_home:
    raise SystemExit("runner home must differ from the managed agent home")
agent_home["hostPath"]["path"] = runner_home
agent_home["hostPath"]["type"] = "DirectoryOrCreate"

with open(path, "w", encoding="utf-8") as destination:
    yaml.safe_dump(values, destination, sort_keys=False)
print(managed_workload)
PY
)"

if [[ ! "${MANAGED_WORKLOAD}" =~ ${dns_name} ]]; then
  echo "Current release has an invalid managed workload name" >&2
  exit 1
fi
if ! kubectl rollout status "deployment/${MANAGED_WORKLOAD}" \
  --namespace "${NAMESPACE}" \
  --timeout 30s >/dev/null; then
  echo "Managed Agent must be Ready before bootstrapping a release runner" >&2
  exit 1
fi

NODE_NAME="$(
  kubectl get "deployment/${MANAGED_WORKLOAD}" \
    --namespace "${NAMESPACE}" \
    --output jsonpath='{.spec.template.spec.nodeSelector.kubernetes\.io/hostname}'
)"
if [[ -z "${NODE_NAME}" ]]; then
  echo "Managed Agent must select an explicit kubernetes.io/hostname node" >&2
  exit 1
fi
DISK_PRESSURE="$(
  kubectl get "node/${NODE_NAME}" \
    --output jsonpath='{.status.conditions[?(@.type=="DiskPressure")].status}'
)"
if [[ "${DISK_PRESSURE}" != "False" ]]; then
  echo "Release runner blocked: node ${NODE_NAME} reports DiskPressure=${DISK_PRESSURE:-unknown}" >&2
  exit 1
fi

read -r ROOT_TOTAL_KB ROOT_AVAILABLE_KB < <(df -Pk / | awk 'NR == 2 {print $2, $4}')
MIN_AVAILABLE_KB=$((5 * 1024 * 1024))
if ((ROOT_AVAILABLE_KB < MIN_AVAILABLE_KB)) ||
  ((ROOT_AVAILABLE_KB * 100 < ROOT_TOTAL_KB * 10)); then
  echo "Release runner blocked: root filesystem requires at least 10% and 5 GiB free" >&2
  exit 1
fi

helm upgrade "${RUNNER_RELEASE}" "${REPO_ROOT}/deploy/helm/doops-agent" \
  --install \
  --atomic \
  --namespace "${NAMESPACE}" \
  --timeout 5m \
  --values "${VALUES_FILE}"

echo "Release runner ready: ${NAMESPACE}/${RUNNER_RELEASE} -> ${IMAGE}"
