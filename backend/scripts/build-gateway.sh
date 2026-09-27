#!/bin/bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SOURCE_ROOT="$(cd "${REPO_ROOT}/.." && pwd)"
cd "${REPO_ROOT}"
mkdir -p bin

SOURCE_COMMIT="${DOOPS_SOURCE_COMMIT:-$(git -C "${SOURCE_ROOT}" rev-parse HEAD)}"
if [[ -n "$(git -C "${SOURCE_ROOT}" status --porcelain --untracked-files=all)" ]]; then
  if [[ -n "${DOOPS_SOURCE_COMMIT:-}" ]]; then
    echo "Error: cannot embed a clean source commit while the source tree is dirty" >&2
    exit 2
  fi
  SOURCE_COMMIT="${SOURCE_COMMIT}-dirty"
fi
if [[ ! "${SOURCE_COMMIT}" =~ ^[0-9a-f]{40}(-dirty)?$ ]]; then
  echo "Error: DOOPS_SOURCE_COMMIT must be a 40-character commit SHA" >&2
  exit 2
fi

(
  cd gateway
  CGO_ENABLED="${CGO_ENABLED:-0}" GOOS="${GOOS:-linux}" GOARCH="${GOARCH:-amd64}" \
    go build \
      -ldflags "-X github.com/user/doops/agent/pkg/gatewaycmd.BuildCommit=${SOURCE_COMMIT}" \
      -o ../bin/doops-gateway .
)

echo "Built: $(pwd)/bin/doops-gateway source-commit=${SOURCE_COMMIT}"
file bin/doops-gateway 2>/dev/null || true
