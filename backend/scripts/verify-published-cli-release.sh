#!/usr/bin/env bash
set -euo pipefail

RELEASE_TAG="${DOOPS_RELEASE_TAG:-}"
RELEASE_COMMIT="${DOOPS_RELEASE_COMMIT:-}"
RELEASE_REPO="${DOOPS_RELEASE_REPO:-l8ai/ai/doops.sh}"
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TEMP_DIR}"' EXIT

if [[ -z "${RELEASE_TAG}" ]]; then
  echo "DOOPS_RELEASE_TAG is required" >&2
  exit 1
fi
if ! printf '%s\n' "${RELEASE_COMMIT}" | grep -Eq '^[0-9a-f]{40}$'; then
  echo "DOOPS_RELEASE_COMMIT must be the full release commit SHA" >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required to verify CLI release assets" >&2
  exit 1
fi

download_asset() {
  local name="$1"
  curl --fail --location --silent --show-error \
    --output "${TEMP_DIR}/${name}" \
    "https://cnb.cool/${RELEASE_REPO}/-/releases/download/${RELEASE_TAG}/${name}"
}

sha256_file() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${file}" | awk '{print $1}'
    return
  fi
  shasum -a 256 "${file}" | awk '{print $1}'
}

download_asset checksums.txt
for artifact in doops-darwin-amd64 doops-darwin-arm64 doops-linux-amd64 doops-linux-arm64; do
  download_asset "${artifact}"
  expected="$(awk -v file="${artifact}" '$2 == file { print $1 }' "${TEMP_DIR}/checksums.txt")"
  actual="$(sha256_file "${TEMP_DIR}/${artifact}")"
  if [[ -z "${expected}" || "${actual}" != "${expected}" ]]; then
    echo "Published CLI checksum mismatch: ${artifact}" >&2
    exit 1
  fi
  for marker in "${RELEASE_TAG}" "${RELEASE_COMMIT}" "doops.sh/cli-contract/v3"; do
    if ! grep -aFq "${marker}" "${TEMP_DIR}/${artifact}"; then
      echo "Published CLI identity marker missing: ${artifact}" >&2
      exit 1
    fi
  done
done

chmod 0755 "${TEMP_DIR}/doops-linux-amd64"
identity="$("${TEMP_DIR}/doops-linux-amd64" version)"
if [[ "${identity}" != "version=${RELEASE_TAG} commit=${RELEASE_COMMIT} contract=doops.sh/cli-contract/v3" ]]; then
  echo "Published CLI identity mismatch for doops-linux-amd64" >&2
  exit 1
fi

echo "CNB Release Assets verified for ${RELEASE_TAG}."
