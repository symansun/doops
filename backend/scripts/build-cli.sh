#!/usr/bin/env bash
# Doops CLI 构建脚本
# 默认：构建当前平台到仓库根目录 bin/doops
# --all：额外产出 darwin/linux 的 amd64/arm64 发布二进制到 dist/cli/
set -euo pipefail

usage() {
  cat <<'EOF'
用法:
  bash scripts/build-cli.sh
  bash scripts/build-cli-release.sh

说明:
  默认输出:
    bin/doops

  发布产物写入临时目录，再上传到同名版本的 CNB Release Assets:
    dist/cli/doops-darwin-amd64
    dist/cli/doops-darwin-arm64
    dist/cli/doops-linux-amd64
    dist/cli/doops-linux-arm64
EOF
}

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLI_DIR="${REPO_ROOT}/cli"
DEFAULT_OUT="${DOOPS_CLI_OUT:-${REPO_ROOT}/bin/doops}"
RELEASE_DIR="${DOOPS_CLI_RELEASE_DIR:-${REPO_ROOT}/dist/cli}"
BUILD_ALL=false
CLI_VERSION="${DOOPS_CLI_VERSION:-dev}"
CLI_COMMIT="${DOOPS_CLI_COMMIT:-$(git -C "${REPO_ROOT}" rev-parse HEAD 2>/dev/null || printf unknown)}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --all)
      BUILD_ALL=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

build_one() {
  local goos="$1"
  local goarch="$2"
  local out="$3"

  mkdir -p "$(dirname "$out")"
  (
    cd "${CLI_DIR}"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -buildvcs=false -trimpath \
        -ldflags="-s -w -X main.cliBuildVersion=${CLI_VERSION} -X main.cliBuildCommit=${CLI_COMMIT}" \
        -o "${out}" ./cli/doops/
  )

  chmod +x "${out}"
  local size
  size="$(du -h "${out}" | cut -f1)"
  echo "Built: ${out} (${goos}/${goarch}, ${size})"
}

if [[ "${BUILD_ALL}" == "true" ]]; then
  actual_builder="$(go env GOOS)/$(go env GOARCH) $(go version | awk '{print $3}')"
  expected_builder="linux/amd64 go1.25.14"
  if [[ "${actual_builder}" != "${expected_builder}" ]]; then
    echo "release artifacts require ${expected_builder}; got ${actual_builder}" >&2
    echo "run scripts/build-cli-release.sh instead" >&2
    exit 1
  fi
fi

host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
build_one "${host_goos}" "${host_goarch}" "${DEFAULT_OUT}"

if [[ "${BUILD_ALL}" == "true" ]]; then
  mkdir -p "${RELEASE_DIR}"
  build_one "darwin" "amd64" "${RELEASE_DIR}/doops-darwin-amd64"
  build_one "darwin" "arm64" "${RELEASE_DIR}/doops-darwin-arm64"
  build_one "linux" "amd64" "${RELEASE_DIR}/doops-linux-amd64"
  build_one "linux" "arm64" "${RELEASE_DIR}/doops-linux-arm64"
  # The Gateway runs on its own host; it ships as a release asset next to the
  # CLI instead of riding inside every target's Agent image.
  (
    cd "${REPO_ROOT}/gateway"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
      go build -buildvcs=false -trimpath \
        -ldflags="-X github.com/symansun/doops/backend/agent/pkg/release.Version=${CLI_VERSION} -X github.com/symansun/doops/backend/agent/pkg/release.Commit=${CLI_COMMIT}" \
        -o "${RELEASE_DIR}/doops-gateway-linux-amd64" .
  )
  echo "Built: ${RELEASE_DIR}/doops-gateway-linux-amd64 (linux/amd64)"
  (
    cd "${RELEASE_DIR}"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum doops-darwin-amd64 doops-darwin-arm64 doops-linux-amd64 doops-linux-arm64 doops-gateway-linux-amd64 > checksums.txt
    elif command -v shasum >/dev/null 2>&1; then
      shasum -a 256 doops-darwin-amd64 doops-darwin-arm64 doops-linux-amd64 doops-linux-arm64 doops-gateway-linux-amd64 > checksums.txt
    else
      echo "Neither sha256sum nor shasum is available" >&2
      exit 1
    fi
  )
  echo "Checksums: ${RELEASE_DIR}/checksums.txt"
fi
