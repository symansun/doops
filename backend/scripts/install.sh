#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Doops CLI / Skill 安装脚本（不部署或升级 Agent）

用法:
  bash scripts/install.sh --version v1.2.3 --project /path/to/project

  bash scripts/install.sh \
    --version v1.2.3 \
    --project /path/to/project \
    --target-name jm \
    --target-gateway https://gateway.example.com \
    --target-cluster doops-jm \
    --target-instance jm-228 \
    --target-aliases jy,oilan \
    --use 'JM via gateway'

安装结果:
  CLI:   ~/.local/bin/doops
  Skill: <project>/.agent/skills/doops/SKILL.md
  Skill docs: <project>/.agent/skills/doops/docs/
  Config: ~/.agent/skills/doops/config.json

安装包来源:
  CLI 只从 CNB Release Assets 下载指定 --version 的 doops-<os>-<arch> 与 checksums.txt
  普通用户安装不在用户机器或 target 机器上编译

更新行为:
  不带 --target-* 时只更新 CLI 和 Skill，不改已有 config.json
  带 --target-* 时按 name 新增或更新 canonical config 中的对应 target，保留其它 targets 和 llm 配置

Skill 维护规则:
  权威源码在 doops.sh 仓库 backend/skills/SKILL.md
  install.sh 从下载 CLI 内嵌 commit 的本地 Git 对象读取 skill/docs，不复制当前工作区
  本地缺少对应提交时先获取该发布提交再重试；安装器不会自动 fetch 或混装其它版本

连接模式:
 标准入口只有 Gateway 模式:
    --target-gateway + --target-cluster/--target-instance
  target 配置只保存路由；安装后通过 doops login 为整个 Gateway 建立统一登录态。

如果只是临时连受控实验 gateway，且它还没有 TLS，可以在客户端显式设置 `DOOPS_ALLOW_INSECURE_GATEWAY=1`，但不要把它当成生产标准写法。
EOF
}

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLI_OUT="${HOME}/.local/bin/doops"
PROJECT_PATH="$(pwd)"
RELEASE_TAG=""
CLI_TEMP_DIR=""
RELEASE_SOURCE_DIR=""
TARGET_NAME=""
TARGET_GATEWAY=""
TARGET_CLUSTER=""
TARGET_INSTANCE=""
TARGET_ALIASES=""
TARGET_USE="Doops managed node"

detect_host_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "${os}" in
    linux|darwin) ;;
    *)
      echo "unsupported-os"
      return
      ;;
  esac

  case "${arch}" in
    x86_64|amd64)
      arch="amd64"
      ;;
    aarch64|arm64)
      arch="arm64"
      ;;
    *)
      echo "unsupported-arch"
      return
      ;;
  esac

  printf '%s-%s\n' "${os}" "${arch}"
}

stage_release_sources() {
  local commit="$1" repository_root archive staged
  repository_root="$(cd "${REPO_ROOT}/.." && pwd)"
  if ! command -v git >/dev/null 2>&1; then
    echo "git is required to stage Skill sources from the release commit" >&2
    exit 1
  fi
  if ! git -C "${repository_root}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "Installer source is not inside a git worktree; cannot resolve release Skill sources" >&2
    exit 1
  fi
  if ! git -C "${repository_root}" cat-file -e "${commit}^{commit}" >/dev/null 2>&1; then
    echo "Release commit ${commit} is unavailable in local git objects" >&2
    exit 1
  fi
  if ! git -C "${repository_root}" cat-file -e "${commit}:backend/skills/SKILL.md" >/dev/null 2>&1; then
    echo "Release commit ${commit} is missing backend/skills/SKILL.md" >&2
    exit 1
  fi
  if ! git -C "${repository_root}" ls-tree -r --name-only "${commit}" -- backend/docs/ |
    awk -F/ 'NF == 3 && $1 == "backend" && $2 == "docs" && $3 ~ /\.md$/ { found=1 } END { exit found ? 0 : 1 }'; then
    echo "Release commit ${commit} is missing backend/docs/*.md" >&2
    exit 1
  fi

  archive="${CLI_TEMP_DIR}/release-sources.tar"
  staged="${CLI_TEMP_DIR}/release-sources"
  mkdir -p "${staged}"
  if ! git -C "${repository_root}" archive \
    --format=tar \
    --output="${archive}" \
    "${commit}" \
    -- \
    backend/skills/SKILL.md \
    backend/docs; then
    echo "Failed to stage Skill sources from release commit ${commit}" >&2
    exit 1
  fi
  if ! tar -xf "${archive}" -C "${staged}"; then
    echo "Failed to unpack Skill sources from release commit ${commit}" >&2
    exit 1
  fi
  if [[ ! -f "${staged}/backend/skills/SKILL.md" ]]; then
    echo "Release commit ${commit} is missing backend/skills/SKILL.md" >&2
    exit 1
  fi
  if ! compgen -G "${staged}/backend/docs/*.md" >/dev/null; then
    echo "Release commit ${commit} is missing backend/docs/*.md" >&2
    exit 1
  fi
  RELEASE_SOURCE_DIR="${staged}"
}

sha256_file() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${file}" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${file}" | awk '{print $1}'
    return
  fi
  echo "Neither sha256sum nor shasum is available" >&2
  return 1
}

install_cli() {
  local platform artifact checksums expected actual candidate replacement receipt_dir receipt
  local identity release commit contract validation_output
  platform="$(detect_host_platform)"
  if [[ "${platform}" == unsupported-* ]]; then
    cat >&2 <<EOF
  Unsupported platform: ${platform}
  Maintainers must publish a CLI release asset named:
    doops-<os>-<arch>
EOF
    exit 1
  fi

  if [[ -z "${RELEASE_TAG}" ]]; then
    echo "--version is required; CLI releases are immutable CNB Release Assets." >&2
    exit 1
  fi

  if ! command -v curl >/dev/null 2>&1; then
    echo "curl is required to download CLI release assets" >&2
    exit 1
  fi

  artifact="doops-${platform}"
  CLI_TEMP_DIR="$(mktemp -d)"
  trap 'rm -rf "${CLI_TEMP_DIR:-}"' EXIT
  checksums="${CLI_TEMP_DIR}/checksums.txt"
  candidate="${CLI_TEMP_DIR}/${artifact}"

  curl --fail --location --silent --show-error \
    --output "${checksums}" \
    "https://cnb.cool/l8ai/ai/doops.sh/-/releases/download/${RELEASE_TAG}/checksums.txt"
  curl --fail --location --silent --show-error \
    --output "${candidate}" \
    "https://cnb.cool/l8ai/ai/doops.sh/-/releases/download/${RELEASE_TAG}/${artifact}"

  expected="$(awk -v file="${artifact}" '$2 == file { print $1 }' "${checksums}")"
  if [[ -z "${expected}" ]]; then
    echo "Release checksum is missing for ${artifact} in ${RELEASE_TAG}" >&2
    exit 1
  fi
  actual="$(sha256_file "${candidate}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "Release checksum mismatch for ${artifact} in ${RELEASE_TAG}" >&2
    exit 1
  fi
  chmod 0755 "${candidate}"
  if ! identity="$("${candidate}" version 2>/dev/null)"; then
    echo "Release CLI identity check failed for ${artifact}" >&2
    exit 1
  fi
  release="$(printf '%s\n' "${identity}" | awk '{for (i=1; i<=NF; i++) if ($i ~ /^release=/) {sub(/^release=/, "", $i); print $i}}')"
  commit="$(printf '%s\n' "${identity}" | awk '{for (i=1; i<=NF; i++) if ($i ~ /^commit=/) {sub(/^commit=/, "", $i); print $i}}')"
  if ! contract="$("${candidate}" --contract 2>/dev/null)"; then
    echo "Release CLI contract check failed for ${artifact}" >&2
    exit 1
  fi
  if [[ "${release}" != "${RELEASE_TAG}" ]] ||
    ! printf '%s\n' "${commit}" | grep -Eq '^[0-9a-f]{40}$' ||
    [[ "${contract}" != "doops.sh/cli-contract/v3" ]]; then
    echo "Release CLI identity mismatch for ${artifact}: expected release ${RELEASE_TAG} and contract v3" >&2
    exit 1
  fi

  stage_release_sources "${commit}"

  if ! validation_output="$(DOOPS_CONFIG= "${candidate}" list 2>&1)"; then
    echo "Existing doops config rejected by release CLI:" >&2
    printf '%s\n' "${validation_output}" >&2
    exit 1
  fi

  mkdir -p "$(dirname "${CLI_OUT}")"
  if [[ -d "${CLI_OUT}" ]]; then
    echo "CLI install path is a directory: ${CLI_OUT}" >&2
    exit 1
  fi
  replacement="$(mktemp "$(dirname "${CLI_OUT}")/.doops-install.XXXXXX")"
  install -m 0755 "${candidate}" "${replacement}"
  if ! mv -f "${replacement}" "${CLI_OUT}"; then
    rm -f "${replacement}"
    echo "Failed to replace CLI at ${CLI_OUT}" >&2
    exit 1
  fi
  receipt_dir="${HOME}/.local/share/doops"
  receipt="${receipt_dir}/install.txt"
  mkdir -p "${receipt_dir}"
  chmod 0700 "${receipt_dir}"
  {
    printf 'release=%s\n' "${RELEASE_TAG}"
    printf 'artifact=%s\n' "${artifact}"
    printf 'sha256=%s\n' "${actual}"
    printf 'commit=%s\n' "${commit}"
    printf 'skill_commit=%s\n' "${commit}"
    printf 'contract=%s\n' "${contract}"
    printf 'source=cnb-release-assets\n'
    printf 'path=%s\n' "${CLI_OUT}"
  } >"${receipt}"
  chmod 0600 "${receipt}"
  echo "  CLI installed from CNB Release Assets: ${RELEASE_TAG}/${artifact}"
  echo "  CLI installed to ${CLI_OUT}"
  echo "  Install receipt written to ${receipt}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)
      PROJECT_PATH="${2:-}"
      shift 2
      ;;
    --version)
      RELEASE_TAG="${2:-}"
      shift 2
      ;;
    --target-name)
      TARGET_NAME="${2:-}"
      shift 2
      ;;
    --target-gateway|--gateway)
      TARGET_GATEWAY="${2:-}"
      shift 2
      ;;
    --target-cluster|--cluster)
      TARGET_CLUSTER="${2:-}"
      shift 2
      ;;
    --target-instance|--instance)
      TARGET_INSTANCE="${2:-}"
      shift 2
      ;;
    --target-aliases|--aliases|--alias)
      TARGET_ALIASES="${2:-}"
      shift 2
      ;;
    --use)
      TARGET_USE="${2:-}"
      shift 2
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

PROJECT_PATH="$(cd "${PROJECT_PATH}" && pwd)"
SKILL_DEST="${PROJECT_PATH}/.agent/skills/doops"
CANONICAL_CONFIG_FILE="${HOME}/.agent/skills/doops/config.json"

if [[ -n "${TARGET_GATEWAY}" || -n "${TARGET_NAME}" || -n "${TARGET_CLUSTER}" || -n "${TARGET_INSTANCE}" ]]; then
  if [[ -z "${TARGET_GATEWAY}" || -z "${TARGET_NAME}" || -z "${TARGET_CLUSTER}" || -z "${TARGET_INSTANCE}" ]]; then
    echo "Gateway target installation requires --target-name, --target-gateway, --target-cluster, and --target-instance" >&2
    exit 2
  fi
fi

echo "[1/4] Installing CLI to fixed path"
install_cli

echo "[2/4] Installing doops skill"
mkdir -p "${SKILL_DEST}"
cp "${RELEASE_SOURCE_DIR}/backend/skills/SKILL.md" "${SKILL_DEST}/SKILL.md"
rm -rf "${SKILL_DEST}/docs"
mkdir -p "${SKILL_DEST}/docs"
cp "${RELEASE_SOURCE_DIR}"/backend/docs/*.md "${SKILL_DEST}/docs/"
echo "  Skill installed to ${SKILL_DEST}/SKILL.md"
echo "  Skill docs installed to ${SKILL_DEST}/docs/"

echo "[3/4] Preparing canonical doops config"

if [[ -n "${TARGET_GATEWAY}" ]]; then
  add_args=(add --name "${TARGET_NAME}" --gateway "${TARGET_GATEWAY}" --cluster "${TARGET_CLUSTER}" --instance "${TARGET_INSTANCE}" --use "${TARGET_USE}")
  if [[ -n "${TARGET_ALIASES}" ]]; then
    add_args+=(--aliases "${TARGET_ALIASES}")
  fi
  (cd "${PROJECT_PATH}" && DOOPS_CONFIG= "${CLI_OUT}" "${add_args[@]}")
  echo "  Gateway config upserted in ${CANONICAL_CONFIG_FILE}"
else
  cat <<EOF
  Canonical config not changed automatically.
  To make the project immediately connectable in gateway mode, rerun with:
    --target-name --target-gateway --target-cluster --target-instance [--use]
EOF
fi

echo "[4/4] Done"
cat <<EOF

Installed paths:
  CLI:    ${CLI_OUT}
  Skill:  ${SKILL_DEST}/SKILL.md
  Docs:   ${SKILL_DEST}/docs/
  Config: ${CANONICAL_CONFIG_FILE}

Agent deployment was not changed. See:
  ${SKILL_DEST}/docs/CLUSTER_INSTALL_GUIDE.md
This installer uses the canonical config; DOOPS_CONFIG overrides are not used.

If ~/.local/bin is not in PATH, run:
  export PATH="\$HOME/.local/bin:\$PATH"

Quick verify:
  cd "${PROJECT_PATH}"
  ${CLI_OUT} version
  ${CLI_OUT} list
  ${CLI_OUT} -session setup login --target ${TARGET_NAME:-<target-name>}
  ${CLI_OUT} -session smoke exec --target ${TARGET_NAME:-<target-name>} --cmd "hostname"
EOF

if [[ -n "${TARGET_GATEWAY}" ]]; then
  cat <<EOF
  ${CLI_OUT} -session setup targets --target ${TARGET_NAME:-<gateway-target-name>}
EOF
fi
