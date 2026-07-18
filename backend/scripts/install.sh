#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Doops 一键安装脚本

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
  权威源码在 doops.sh 仓库 skills/SKILL.md
  install.sh 只把源码里的 skill/docs 复制到业务项目，不从业务项目反向生成 skill

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
  local platform artifact checksums expected actual candidate replacement
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
  echo "  CLI installed from CNB Release Assets: ${RELEASE_TAG}/${artifact}"
  echo "  CLI installed to ${CLI_OUT}"
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

if [[ -f "${CANONICAL_CONFIG_FILE}" ]]; then
  command -v python3 >/dev/null 2>&1 || {
    echo "python3 is required to validate existing doops config" >&2
    exit 1
  }
  python3 - "${CANONICAL_CONFIG_FILE}" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as handle:
    root = json.load(handle)
if not isinstance(root, dict) or set(root) - {"servers", "llm"}:
    raise SystemExit("existing config must be an object containing only servers and optional llm")
servers = root.get("servers", [])
if not isinstance(servers, list):
    raise SystemExit("existing config servers must be an array")
allowed = {"name", "aliases", "use", "gateway", "cluster", "instance"}
required = {"name", "gateway", "cluster", "instance"}
for index, server in enumerate(servers):
    if not isinstance(server, dict):
        raise SystemExit(f"existing config server[{index}] must be an object")
    unknown = sorted(set(server) - allowed)
    if unknown:
        raise SystemExit(f"existing config server[{index}] contains unsupported fields: {', '.join(unknown)}")
    missing = sorted(key for key in required if not str(server.get(key, "")).strip())
    if missing:
        raise SystemExit(f"existing config server[{index}] is missing Gateway fields: {', '.join(missing)}")
PY
fi

echo "[1/4] Installing CLI to fixed path"
install_cli

echo "[2/4] Installing doops skill"
mkdir -p "${SKILL_DEST}"
cp "${REPO_ROOT}/skills/SKILL.md" "${SKILL_DEST}/SKILL.md"
rm -rf "${SKILL_DEST}/docs"
mkdir -p "${SKILL_DEST}/docs"
cp "${REPO_ROOT}"/docs/*.md "${SKILL_DEST}/docs/"
echo "  Skill installed to ${SKILL_DEST}/SKILL.md"
echo "  Skill docs installed to ${SKILL_DEST}/docs/"

echo "[3/4] Preparing canonical doops config"

if [[ -n "${TARGET_GATEWAY}" ]]; then
  add_args=(add --name "${TARGET_NAME}" --gateway "${TARGET_GATEWAY}" --cluster "${TARGET_CLUSTER}" --instance "${TARGET_INSTANCE}" --use "${TARGET_USE}")
  if [[ -n "${TARGET_ALIASES}" ]]; then
    add_args+=(--aliases "${TARGET_ALIASES}")
  fi
  (cd "${PROJECT_PATH}" && "${CLI_OUT}" "${add_args[@]}")
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

If ~/.local/bin is not in PATH, run:
  export PATH="\$HOME/.local/bin:\$PATH"

Quick verify:
  cd "${PROJECT_PATH}"
  doops list
  doops login --target ${TARGET_NAME:-<target-name>}
  doops -session smoke exec --target ${TARGET_NAME:-<target-name>} --cmd "hostname"
EOF

if [[ -n "${TARGET_GATEWAY}" ]]; then
  cat <<EOF
  doops targets --target ${TARGET_NAME:-<gateway-target-name>}
EOF
fi
