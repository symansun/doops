#!/usr/bin/env bash
set -euo pipefail

set -eu
test "${GITHUB_REF}" = "refs/heads/main"
: "${DOOPS_GATEWAY_TOKEN:?DOOPS_GATEWAY_TOKEN is required}"
: "${DOOPS_GATEWAY_URL:?DOOPS_GATEWAY_URL is required}"
: "${DOOPS_SESSION:?DOOPS_SESSION is required}"
: "${DOOPS_TARGET:?DOOPS_TARGET is required}"
: "${DOOPS_CLUSTER:?DOOPS_CLUSTER is required}"
: "${DOOPS_INSTANCE:?DOOPS_INSTANCE is required}"
ROOT="${PWD}"
export DOOPS_CONFIG="${RUNNER_TEMP:?RUNNER_TEMP is required}/doops-workload/config.json"
mkdir -p "$(dirname "${DOOPS_CONFIG}")" .ci-bin
(
  cd backend/cli
  go build -o "${ROOT}/.ci-bin/doops" ./cli/doops
)
.ci-bin/doops add \
  --name "${DOOPS_TARGET}" \
  --gateway "${DOOPS_GATEWAY_URL}" \
  --cluster "${DOOPS_CLUSTER}" \
  --instance "${DOOPS_INSTANCE}" \
  --use "GitHub Actions workload release"
printf '%s' "${DOOPS_GATEWAY_TOKEN}" |
  .ci-bin/doops -session "${DOOPS_SESSION}" login --target "${DOOPS_TARGET}" --token-stdin
.ci-bin/doops -session "${DOOPS_SESSION}" targets --target "${DOOPS_TARGET}"

set -eu
: "${DOOPS_SESSION:?DOOPS_SESSION is required}"
: "${DOOPS_TARGET:?DOOPS_TARGET is required}"
: "${DOOPS_IMAGE:?DOOPS_IMAGE is required}"
: "${DOOPS_SERVICE_SCOPE:?DOOPS_SERVICE_SCOPE is required}"
: "${DOOPS_HELM_ASSET:?DOOPS_HELM_ASSET remote chart URL is required}"
: "${DOOPS_CHANGE_REASON:?DOOPS_CHANGE_REASON is required}"
: "${DOOPS_ACCEPTANCE:?DOOPS_ACCEPTANCE is required}"
validate_simple_name() {
  case "$1" in
    ""|"."|".."|*[!A-Za-z0-9._-]*)
      echo "invalid $2" >&2
      exit 2
      ;;
  esac
}
validate_single_line() {
  carriage_return="$(printf '\r')"
  if [ -z "$1" ] || [ "$(printf '%s' "$1" | wc -l)" -ne 0 ]; then
    echo "invalid $2" >&2
    exit 2
  fi
  case "$1" in
    *"${carriage_return}"*)
      echo "invalid $2" >&2
      exit 2
      ;;
  esac
}
validate_simple_name "${DOOPS_SESSION}" "DOOPS_SESSION"
validate_simple_name "${DOOPS_TARGET}" "DOOPS_TARGET"
if [ -n "${DOOPS_CREDENTIAL:-}" ]; then
  validate_simple_name "${DOOPS_CREDENTIAL}" "DOOPS_CREDENTIAL"
fi
validate_single_line "${DOOPS_SERVICE_SCOPE}" "DOOPS_SERVICE_SCOPE"
validate_single_line "${DOOPS_HELM_ASSET}" "DOOPS_HELM_ASSET"
validate_single_line "${DOOPS_CHANGE_REASON}" "DOOPS_CHANGE_REASON"
validate_single_line "${DOOPS_ACCEPTANCE}" "DOOPS_ACCEPTANCE"
case "${DOOPS_IMAGE}" in
  *@sha256:*) ;;
  *)
    echo "invalid DOOPS_IMAGE" >&2
    exit 2
    ;;
esac
image_repository="${DOOPS_IMAGE%@sha256:*}"
image_digest="${DOOPS_IMAGE##*@sha256:}"
case "${image_repository}" in
  ""|*[!A-Za-z0-9._:/-]*)
    echo "invalid DOOPS_IMAGE repository" >&2
    exit 2
    ;;
esac
case "${image_digest}" in
  *[!a-f0-9]*)
    echo "invalid DOOPS_IMAGE digest" >&2
    exit 2
    ;;
esac
test "${#image_digest}" -eq 64
case "${DOOPS_HELM_ASSET}" in
  https://*) ;;
  *)
    echo "DOOPS_HELM_ASSET must be an explicit https:// chart URL" >&2
    exit 2
    ;;
esac

instruction="在 ${DOOPS_TARGET} 发布 ${DOOPS_SERVICE_SCOPE}，镜像 ${DOOPS_IMAGE}；使用 chart ${DOOPS_HELM_ASSET}；原因：${DOOPS_CHANGE_REASON}；验收：${DOOPS_ACCEPTANCE}。"
if [ -n "${DOOPS_CREDENTIAL:-}" ]; then
  .ci-bin/doops -session "${DOOPS_SESSION}" ask \
    --target "${DOOPS_TARGET}" \
    --apply \
    --credential "${DOOPS_CREDENTIAL}" \
    --msg "${instruction}"
else
  .ci-bin/doops -session "${DOOPS_SESSION}" ask \
    --target "${DOOPS_TARGET}" \
    --apply \
    --msg "${instruction}"
fi
