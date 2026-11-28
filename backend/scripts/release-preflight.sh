#!/usr/bin/env bash
# Read-only release preflight check before cutting a release tag.
# Runs whitespace check, go vet across all 3 Go modules, CLI cross-platform
# build smoke, SKILL.md byte-size budget, and advisory CHANGELOG cut checklist.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: bash backend/scripts/release-preflight.sh [options]

Options:
  --prev-tag <tag>     Previous release tag for changelog-cut-checklist.py
  --next-tag <tag>     Candidate release tag (for example v0.3.72)
  --skip-cross-build   Skip darwin/linux/windows CLI cross-compile smoke
  --check-gofmt        Also enforce `gofmt -l .` across all 3 Go modules (recommended on CI Go 1.25)
  -h, --help           Show this help message
EOF
}

PREV_TAG=""
NEXT_TAG=""
SKIP_CROSS_BUILD=0
CHECK_GOFMT=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prev-tag)
      [[ $# -ge 2 ]] || { echo "error: --prev-tag requires a value" >&2; exit 2; }
      PREV_TAG="$2"
      shift 2
      ;;
    --next-tag)
      [[ $# -ge 2 ]] || { echo "error: --next-tag requires a value" >&2; exit 2; }
      NEXT_TAG="$2"
      shift 2
      ;;
    --skip-cross-build)
      SKIP_CROSS_BUILD=1
      shift
      ;;
    --check-gofmt)
      CHECK_GOFMT=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

ROOT="$(git rev-parse --show-toplevel)"
cd "${ROOT}"

echo "==> 1. git diff --check"
git diff --check

echo "==> 2. SKILL.md size budget (<= 12288 bytes)"
skill_bytes="$(wc -c < skills/doops/SKILL.md | tr -d '[:space:]')"
echo "skills/doops/SKILL.md: ${skill_bytes} bytes"
if [[ "${skill_bytes}" -gt 12288 ]]; then
  echo "error: skills/doops/SKILL.md exceeds 12288 bytes (${skill_bytes})" >&2
  exit 1
fi

if [[ "${CHECK_GOFMT}" -eq 1 ]]; then
  echo "==> 3a. gofmt across backend/agent, backend/gateway, backend/cli"
  for mod in backend/agent backend/gateway backend/cli; do
    unformatted="$(cd "${mod}" && env -u GOROOT gofmt -l .)"
    if [[ -n "${unformatted}" ]]; then
      echo "error: unformatted Go files in ${mod}:" >&2
      printf '%s\n' "${unformatted}" >&2
      exit 1
    fi
  done
fi

echo "==> 3. go vet across backend/agent, backend/gateway, backend/cli"
for mod in backend/agent backend/gateway backend/cli; do
  (cd "${mod}" && env -u GOROOT go vet ./...)
done

if [[ "${SKIP_CROSS_BUILD}" -eq 0 ]]; then
  echo "==> 4. CLI cross-platform compile smoke (darwin/arm64, linux/amd64, windows/amd64)"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "${tmp_dir}"' EXIT
  (
    cd backend/cli
    env -u GOROOT GOOS=darwin GOARCH=arm64 go build -o "${tmp_dir}/doops-darwin-arm64" ./cli/doops
    env -u GOROOT GOOS=linux GOARCH=amd64 go build -o "${tmp_dir}/doops-linux-amd64" ./cli/doops
    env -u GOROOT GOOS=windows GOARCH=amd64 go build -o "${tmp_dir}/doops-windows-amd64.exe" ./cli/doops
  )
fi

if [[ -n "${PREV_TAG}" && -n "${NEXT_TAG}" ]]; then
  echo "==> 5. Advisory CHANGELOG checklist (${PREV_TAG}..${NEXT_TAG})"
  python3 backend/scripts/changelog-cut-checklist.py "${PREV_TAG}" "${NEXT_TAG}"
elif [[ -n "${PREV_TAG}" || -n "${NEXT_TAG}" ]]; then
  echo "error: --prev-tag and --next-tag must be passed together" >&2
  exit 2
fi

echo "Release preflight passed."
