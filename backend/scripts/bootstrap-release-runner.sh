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
if [[ ! "${RUNNER_HOME}" =~ ^/[A-Za-z0-9._/-]+$ ]] || [[ "${RUNNER_HOME}" == *"/../"* ]]; then
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

helm get values "${MANAGED_RELEASE}" \
  --namespace "${NAMESPACE}" \
  --output yaml >"${VALUES_FILE}"

python3 - "${VALUES_FILE}" "${RUNNER_WORKLOAD}" "${RUNNER_INSTANCE}" "${RUNNER_HOME}" "${IMAGE_REPOSITORY}" "${IMAGE_DIGEST}" <<'PY'
import sys

import yaml

path, workload, instance, runner_home, repository, digest = sys.argv[1:]
with open(path, encoding="utf-8") as source:
    values = yaml.safe_load(source)

if values["workload"]["name"] == workload:
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
PY

helm upgrade "${RUNNER_RELEASE}" "${REPO_ROOT}/deploy/helm/doops-agent" \
  --install \
  --atomic \
  --namespace "${NAMESPACE}" \
  --timeout 5m \
  --values "${VALUES_FILE}"

echo "Release runner ready: ${NAMESPACE}/${RUNNER_RELEASE} -> ${IMAGE}"
