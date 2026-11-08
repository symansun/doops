#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
DoOps 一键安装脚本

用法:
  # 默认：从 GitHub Release 下载 CLI（含 sha256 校验）和 Skill 包，无需克隆仓库
  bash scripts/install.sh --project /path/to/project

  # 指定版本（默认 latest）
  bash scripts/install.sh --project /path/to/project --version v0.2.2

  # 源码模式：使用仓库内预构建包，要求完整仓库检出
  bash scripts/install.sh --project /path/to/project --from-source

  bash scripts/install.sh \
    --project /path/to/project \
    --target-name prod \
    --target-gateway https://gateway.example.com \
    --target-cluster prod \
    --target-instance master-1 \
    --target-aliases p \
    --target-token 'gateway-user-token' \
    --use 'prod via gateway'

安装结果:
  CLI:   ~/.local/bin/doops
  Skill: <project>/.agent/skills/doops/SKILL.md
  Skill docs: <project>/.agent/skills/doops/docs/
  Config: ~/.agent/skills/doops/config.json

安装包来源:
  默认从 GitHub Release (github.com/symansun/doops/releases) 下载:
    - CLI 二进制 doops-<os>-<arch>，用 checksums.txt 做 sha256 校验
    - Skill 包 doops-skill.tar.gz（SKILL.md + docs）
  --from-source 使用仓库内 skills/doops-cli/bin/doops-<os>-<arch> 预构建包，
  不在用户机器或 target 机器上编译

更新行为:
  不带 --target-* 时只更新 CLI 和 Skill，不改已有 config.json
  带 --target-* 时按 name 新增或更新 canonical config 中的对应 target，保留其它 targets 和 llm 配置

Skill 维护规则:
  权威源码在 DoOps 仓库 skills/SKILL.md
  install.sh 只把源码里的 skill/docs 复制到业务项目，不从业务项目反向生成 skill

连接模式:
  标准入口只有 Gateway 模式:
    --target-gateway + --target-cluster/--target-instance + --target-token
  --target-token 是 gateway user token。
  --target-ip 是遗留直连参数，install.sh 不再用它写入标准配置。

如果只是临时连受控实验 gateway，且它还没有 TLS，可以在客户端显式设置 `DOOPS_ALLOW_INSECURE_GATEWAY=1`，但不要把它当成生产标准写法。
EOF
}

REPO_ROOT="$(cd "$(dirname "$0")/.." 2>/dev/null && pwd || true)"
RELEASE_REPO="https://github.com/symansun/doops/releases"
CLI_OUT="${HOME}/.local/bin/doops"
PROJECT_PATH="$(pwd)"
TARGET_NAME=""
TARGET_IP=""
TARGET_GATEWAY=""
TARGET_CLUSTER="default"
TARGET_INSTANCE=""
TARGET_ALIASES=""
TARGET_TOKEN=""
TARGET_USE="DoOps managed node"
FROM_SOURCE="false"
RELEASE_TAG=""

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

release_base_url() {
  if [[ -n "${RELEASE_TAG}" ]]; then
    printf '%s/download/%s\n' "${RELEASE_REPO}" "${RELEASE_TAG}"
  else
    printf '%s/latest/download\n' "${RELEASE_REPO}"
  fi
}

download() {
  local url="$1" dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fSL --retry 3 -o "${dest}" "${url}"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "${dest}" "${url}"
  else
    echo "需要 curl 或 wget 来下载 Release 产物" >&2
    exit 1
  fi
}

verify_sha256() {
  local file="$1" checksums="$2" name="$3"
  local expected actual
  expected_line="$(grep -E "[[:space:]]${name}\$" "${checksums}" || true)"
  if [[ -z "${expected_line}" ]]; then
    echo "checksums.txt 中找不到 ${name} 的校验和" >&2
    exit 1
  fi
  expected_line="$(printf '%s\n' "${expected_line}" | head -1)"
  expected="$(printf '%s\n' "${expected_line}" | awk '{print $1}')"
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "${file}" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "${file}" | awk '{print $1}')"
  else
    echo "需要 sha256sum 或 shasum 来校验 Release 产物" >&2
    exit 1
  fi
  if [[ "${expected}" != "${actual}" ]]; then
    echo "sha256 校验失败: ${name}" >&2
    echo "  expected: ${expected}" >&2
    echo "  actual:   ${actual}" >&2
    exit 1
  fi
  echo "  sha256 校验通过: ${name}" >&2
}

install_cli_from_source() {
  local platform prebuilt
  platform="$(detect_host_platform)"
  if [[ "${platform}" == unsupported-* ]]; then
    cat >&2 <<EOF
  Unsupported platform: ${platform}
  Maintainers must add a prebuilt CLI artifact under:
    ${REPO_ROOT}/skills/doops-cli/bin/doops-<os>-<arch>
EOF
    exit 1
  fi
  prebuilt="${REPO_ROOT}/skills/doops-cli/bin/doops-${platform}"

  mkdir -p "$(dirname "${CLI_OUT}")"

  if [[ -f "${prebuilt}" ]]; then
    cp "${prebuilt}" "${CLI_OUT}"
    chmod +x "${CLI_OUT}"
    echo "  CLI installed from prebuilt artifact: ${prebuilt}"
    echo "  CLI installed to ${CLI_OUT}"
    return
  fi

  cat >&2 <<EOF
  Prebuilt CLI artifact not found for ${platform}: ${prebuilt}

  User install does not compile doops locally.
  Maintainers must run scripts/build-cli.sh --all and commit the generated
  skills/doops-cli/bin/doops-${platform} artifact to the fixed DoOps repo.
EOF
  exit 1
}

# 默认路径：从 GitHub Release 下载 CLI 并做 sha256 校验。
install_cli_from_release() {
  local platform base tmp
  platform="$(detect_host_platform)"
  if [[ "${platform}" == unsupported-* ]]; then
    echo "  Unsupported platform: ${platform}" >&2
    exit 1
  fi
  base="$(release_base_url)"
  tmp="$(mktemp -d /tmp/doops-install.XXXXXX)"

  echo "  下载 doops-${platform} (${base})"
  download "${base}/doops-${platform}" "${tmp}/doops-${platform}"
  download "${base}/checksums.txt" "${tmp}/checksums.txt"
  verify_sha256 "${tmp}/doops-${platform}" "${tmp}/checksums.txt" "doops-${platform}"

  mkdir -p "$(dirname "${CLI_OUT}")"
  cp "${tmp}/doops-${platform}" "${CLI_OUT}"
  chmod +x "${CLI_OUT}"
  rm -rf "${tmp}"
  echo "  CLI installed to ${CLI_OUT}"
}

# 默认路径：从 GitHub Release 下载 Skill 包并解压到临时目录，输出解压后的源码根。
# 注意：本函数 stdout 只输出路径，日志一律走 stderr。
fetch_skill_bundle() {
  local base tmp
  base="$(release_base_url)"
  tmp="$(mktemp -d /tmp/doops-skill.XXXXXX)"
  echo "  下载 doops-skill.tar.gz (${base})" >&2
  download "${base}/doops-skill.tar.gz" "${tmp}/doops-skill.tar.gz"
  download "${base}/checksums.txt" "${tmp}/checksums.txt"
  verify_sha256 "${tmp}/doops-skill.tar.gz" "${tmp}/checksums.txt" "doops-skill.tar.gz"
  tar -xzf "${tmp}/doops-skill.tar.gz" -C "${tmp}"
  rm -f "${tmp}/doops-skill.tar.gz" "${tmp}/checksums.txt"
  printf '%s\n' "${tmp}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)
      PROJECT_PATH="${2:-}"
      shift 2
      ;;
    --from-source)
      FROM_SOURCE="true"
      shift
      ;;
    --version)
      RELEASE_TAG="${2:-}"
      shift 2
      ;;
    --target-name)
      TARGET_NAME="${2:-}"
      shift 2
      ;;
    --target-ip)
      TARGET_IP="${2:-}"
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
    --target-token)
      TARGET_TOKEN="${2:-}"
      shift 2
      ;;
    --ssh-user)
      SSH_USER="${2:-}"
      shift 2
      ;;
    --ssh-port)
      SSH_PORT="${2:-}"
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

# SRC_ROOT 指向包含 skills/SKILL.md 与 docs/ 的源码根：
# --from-source 用仓库检出；默认从 Release 下载 skill 包。
SRC_ROOT=""
SKILL_TMP=""
if [[ "${FROM_SOURCE}" == "true" ]]; then
  if [[ -z "${REPO_ROOT}" || ! -f "${REPO_ROOT}/skills/SKILL.md" ]]; then
    echo "--from-source 需要完整的 DoOps 仓库检出（未找到 skills/SKILL.md）" >&2
    exit 1
  fi
  SRC_ROOT="${REPO_ROOT}"
else
  SKILL_TMP="$(fetch_skill_bundle)"
  SRC_ROOT="${SKILL_TMP}"
fi

echo "[1/4] Installing CLI to fixed path"
if [[ "${FROM_SOURCE}" == "true" ]]; then
  install_cli_from_source
else
  install_cli_from_release
fi

echo "[2/4] Installing doops skill"
mkdir -p "${SKILL_DEST}"
cp "${SRC_ROOT}/skills/SKILL.md" "${SKILL_DEST}/SKILL.md"
rm -rf "${SKILL_DEST}/docs"
mkdir -p "${SKILL_DEST}/docs"
cp "${SRC_ROOT}"/docs/*.md "${SKILL_DEST}/docs/"
echo "  Skill installed to ${SKILL_DEST}/SKILL.md"
echo "  Skill docs installed to ${SKILL_DEST}/docs/"
if [[ -n "${SKILL_TMP}" ]]; then
  rm -rf "${SKILL_TMP}"
fi

echo "[3/4] Preparing canonical doops config"

if [[ -n "${TARGET_GATEWAY}" && -n "${TARGET_IP}" ]]; then
  cat >&2 <<EOF
  Refusing ambiguous config: standard config must be gateway mode only.
EOF
  exit 1
fi

if [[ -n "${TARGET_IP}" ]]; then
  cat >&2 <<EOF
  Refusing legacy direct config: install.sh only writes gateway targets.
  Use --target-gateway --target-cluster --target-instance --target-token.
EOF
  exit 1
fi

if [[ -n "${TARGET_GATEWAY}" && -n "${TARGET_NAME}" && -n "${TARGET_TOKEN}" ]]; then
  if [[ -z "${TARGET_INSTANCE}" ]]; then
    TARGET_INSTANCE="${TARGET_NAME}"
  fi
  add_args=(add --name "${TARGET_NAME}" --gateway "${TARGET_GATEWAY}" --cluster "${TARGET_CLUSTER}" --instance "${TARGET_INSTANCE}" --token "${TARGET_TOKEN}" --use "${TARGET_USE}")
  if [[ -n "${TARGET_ALIASES}" ]]; then
    add_args+=(--aliases "${TARGET_ALIASES}")
  fi
  (cd "${PROJECT_PATH}" && "${CLI_OUT}" "${add_args[@]}")
  echo "  Gateway config upserted in ${CANONICAL_CONFIG_FILE}"
else
  cat <<EOF
  Canonical config not changed automatically.
  To make the project immediately connectable in gateway mode, rerun with:
    --target-name --target-gateway --target-cluster --target-instance --target-token [--use]
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
  doops -session smoke exec --target ${TARGET_NAME:-<target-name>} --cmd "hostname"
EOF

if [[ -n "${TARGET_GATEWAY}" ]]; then
  cat <<EOF
  doops targets --target ${TARGET_NAME:-<gateway-target-name>}
EOF
fi
