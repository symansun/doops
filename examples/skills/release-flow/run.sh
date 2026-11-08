#!/usr/bin/env bash
# 发布规范示例（release-flow）：把团队 DevOps 发布规范固化成一条命令。
#
# 规范：
#   1. 只发布 git tag（--version vX.Y.Z），不发游离分支；
#   2. 测试/预发环境（--staging-target）直接发布 + 健康检查；
#   3. 生产环境（--prod-target）必须走 gateway 审批流：
#      发起后被审批闸拦截并生成审批单（apr_...），审批人 approve 后，
#      脚本重放同一条命令并附带 --approval 才真正执行；
#   4. 全程同一 session，审计日志里 staging/prod 两段可对照回放。
#
# 目标节点假设：应用位于 git 仓库目录（--app-dir），用 systemd 服务（--service）承载，
# 发布后通过 --health-url 做健康检查。
#
# 用法：
#   bash run.sh --version v1.4.2 \
#     --staging-target staging --prod-target prod \
#     [--app-dir /srv/app] [--service myapp] \
#     [--health-url http://127.0.0.1:8080/health] [--session release-flow]
#
# 前置条件：
#   - 本机已 doops login 到 staging 与 prod 两个 target；
#   - gateway 以 -approval-targets "prod/*"（或等价 glob）启动，使生产 target 命中审批管控；
#   - 审批人持有 admin 授权的账号（doops admin approve）。
set -euo pipefail

VERSION=""
STAGING_TARGET=""
PROD_TARGET=""
APP_DIR="/srv/app"
SERVICE="myapp"
HEALTH_URL=""
SESSION="release-flow"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)        VERSION="${2:-}"; shift 2 ;;
    --staging-target) STAGING_TARGET="${2:-}"; shift 2 ;;
    --prod-target)    PROD_TARGET="${2:-}"; shift 2 ;;
    --app-dir)        APP_DIR="${2:-}"; shift 2 ;;
    --service)        SERVICE="${2:-}"; shift 2 ;;
    --health-url)     HEALTH_URL="${2:-}"; shift 2 ;;
    --session)        SESSION="${2:-}"; shift 2 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

[[ "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "--version 必须是 tag（如 v1.4.2），不允许发布游离分支" >&2; exit 2; }
[[ -n "$STAGING_TARGET" && -n "$PROD_TARGET" ]] || { echo "必须指定 --staging-target 与 --prod-target" >&2; exit 2; }

# 发布命令：申请与重放必须逐字节一致（gateway 按参数 hash 校验审批单）。
DEPLOY_CMD="cd ${APP_DIR} && git fetch --tags && git checkout ${VERSION} && systemctl restart ${SERVICE}"

deploy() { # $1=target  额外参数透传（如 --approval）
  local target="$1"; shift
  doops -session "$SESSION" exec --target "$target" --cmd "$DEPLOY_CMD" "$@"
}

health_check() { # $1=target
  [[ -n "$HEALTH_URL" ]] || { echo "(未配置 --health-url，跳过健康检查)"; return 0; }
  doops -session "$SESSION" exec --target "$1" --cmd "curl -fsS --max-time 5 '${HEALTH_URL}' >/dev/null && echo healthy"
}

echo "==> [1/4] 预发发布 ${VERSION} -> ${STAGING_TARGET}"
deploy "$STAGING_TARGET"
health_check "$STAGING_TARGET"
echo "✅ 预发发布完成"

echo "==> [2/4] 生产发布 ${VERSION} -> ${PROD_TARGET}（触发审批闸）"
set +e
out="$(deploy "$PROD_TARGET" 2>&1)"
set -e
if [[ "$out" != *"approval required"* ]]; then
  # 未被拦截说明该 target 未命中审批管控 —— 规范要求生产必须管控，给出警告后继续。
  echo "$out"
  echo "⚠️  生产 target 未命中审批管控（gateway 未配置 -approval-targets？），已直接执行。请核查配置！" >&2
  health_check "$PROD_TARGET"
  echo "✅ 发布完成（未经审批，存在合规风险）"
  exit 0
fi
APR_ID="$(echo "$out" | grep -o 'apr_[a-f0-9]*' | head -1)"
[[ -n "$APR_ID" ]] || { echo "$out" >&2; echo "未解析到审批单 ID" >&2; exit 1; }
echo "审批单已创建: ${APR_ID}"
echo "请审批人执行: doops admin approve --id ${APR_ID}"

echo "==> [3/4] 等待审批（最长 10 分钟，轮询审批单状态）"
approved="false"
for _ in $(seq 1 60); do
  line="$(doops admin approvals list --target "$PROD_TARGET" 2>/dev/null | grep "$APR_ID" || true)"
  if [[ "$line" == *rejected* ]]; then
    echo "❌ 审批单 ${APR_ID} 已被拒绝，发布中止" >&2; exit 1
  fi
  if [[ "$line" == *approved* ]]; then
    approved="true"; break
  fi
  sleep 10
done
[[ "$approved" == "true" ]] || { echo "等待审批超时，发布中止（审批单 ${APR_ID} 仍 pending，可批准后重放）" >&2; exit 1; }
echo "审批单 ${APR_ID} 已通过"

echo "==> [4/4] 携带审批单重放生产发布"
deploy "$PROD_TARGET" --approval "$APR_ID"
health_check "$PROD_TARGET"
echo "✅ 生产发布完成（${VERSION} -> ${PROD_TARGET}，审批单 ${APR_ID}）"
