#!/usr/bin/env sh
# SB-01 内置巡检 Skill 包校验器（POSIX sh，macOS / BSD 兼容；不依赖 bash 4、不依赖 GNU coreutils 特性）
#
# 一条命令跑完全部内置巡检 Skill 包：
#   A0. 静态契约（全部 8 包，含无脚本的 database / ssh-host）：SKILL.md、frontmatter 的
#       name/description、无绝对路径与 ..、required_mcp 对齐 spec 2.4、附属文件 ≤16 个且 ≤1 MiB、
#       scripts/*.sh 可执行（spec 2.1 / 2.2 / 2.4）
#   A. 静态：6 个脚本包 sh -n + shellcheck -s sh
#   B. docker 真跑：python3 正路径 + 无 python3 降级路径 + app-web 凭据脱敏场景
#   C. 协议断言（spec 4.1）：status ∈ {pass,warning,critical}、status/summary/metrics/checks 齐备、
#      metrics/checks 字段与命名、无 status=skipped、单条 ≤256 KiB、无 userinfo 凭据
#   D. 验收红线 2：同盘多挂载点 disk_total_bytes 不翻倍（4 个去重夹具）+
#      container-runtime 正常/异常路径（2 个 shim 夹具）
#   E. SF-08 鲁棒性矩阵：畸形环境下真跑 6 包与 vm-host 专项夹具（超长单行、非 UTF-8 字节、
#      C/ja locale、TZ、逐命令缺失 df/free/awk/python3、/proc/meminfo 与 /proc/loadavg 异常、
#      符号链接环、含空格与中文的挂载路径、5000 挂载点），断言退出码 0 且产物仍为合法协议 JSON，
#      并逐场景给出「是否降级 + 降级理由」结论
#
# 用法：
#   sh verify.sh                跑全部脚本包（夹具与鲁棒性矩阵始终全跑）
#   sh verify.sh <slug> [...]   只跑指定脚本包
#   SB01_STATIC_ONLY=1 sh verify.sh   只跑 A0（不需要 docker/python/shellcheck）
#   SB01_ROBUST_ONLY=1 sh verify.sh   只跑 E 鲁棒性矩阵
#
# 环境变量：
#   SB01_STATIC_ONLY  1 = 只跑 A0 静态契约层
#   SB01_ROBUST_ONLY  1 = 只跑 E 鲁棒性矩阵
#   SB01_IMG_PYTHON   含 python3 的镜像（默认 doops-t2-agent:v0.3.21）
#   SB01_IMG_SHELL    无 python3 的镜像（默认 alpine:3）
#   SB01_DOCKER / SB01_PYTHON / SB01_SHELLCHECK   工具路径覆盖
#
# 缺工具或镜像一律显式 FAIL，不静默跳过。退出码 0 = 全部通过。

set -u

VROOT=$(cd "$(dirname "$0")" && pwd)
REPO=$VROOT
while [ ! -f "$REPO/AGENTS.md" ] && [ "$REPO" != "/" ]; do
  REPO=$(dirname "$REPO")
done
W="$REPO/backend/agent/internal/server/skills_builtin"
ASSERT="$VROOT/assert_payload.py"
FIX="$VROOT/fixtures"
ROBUST="$FIX/robust"
ROBUST_CHECK="$VROOT/robust_check.py"

DOCKER=${SB01_DOCKER:-docker}
PY=${SB01_PYTHON:-python3}
SHELLCHECK=${SB01_SHELLCHECK:-shellcheck}
IMG_PY=${SB01_IMG_PYTHON:-doops-t2-agent:v0.3.21}
IMG_SH=${SB01_IMG_SHELL:-alpine:3}
CANARY=sb01-canary-pass

ALL_SLUGS="inspect-vm-host inspect-container-runtime inspect-k8s-cluster inspect-app-web inspect-package-vuln inspect-proxy-evasion"
ALL_PKGS="inspect-vm-host inspect-container-runtime inspect-k8s-cluster inspect-app-web inspect-package-vuln inspect-proxy-evasion inspect-database inspect-ssh-host"
STATIC_ONLY=${SB01_STATIC_ONLY:-0}
ROBUST_ONLY=${SB01_ROBUST_ONLY:-0}
ROBUST_ALL_PKGS="inspect-vm-host;inspect-container-runtime;inspect-k8s-cluster;inspect-app-web;inspect-package-vuln;inspect-proxy-evasion"
ROBUST_ALL_FALLBACK=$(printf '%s\n' "$ROBUST_ALL_PKGS" | tr ';' '\n' | sed 's/$/:status=warning,degraded=1/' | tr '\n' ';')
if [ "$#" -gt 0 ]; then
  SLUGS="$*"
else
  SLUGS="$ALL_SLUGS"
fi

TMP=$(mktemp -d 2>/dev/null) || TMP=$(mktemp -d -t sb01verify)
trap 'rm -rf "$TMP"' EXIT INT TERM
ROWS="$TMP/rows"
: > "$ROWS"
TOTAL=0
PASSED=0
FAILED=0

now() { date +%s; }

record() { # status name seconds reason
  TOTAL=$((TOTAL + 1))
  if [ "$1" = PASS ]; then
    PASSED=$((PASSED + 1))
  else
    FAILED=$((FAILED + 1))
  fi
  printf '%s|%s|%s|%s\n' "$2" "$1" "$3" "$4" >> "$ROWS"
  printf '  [%s] %s %s\n' "$1" "$2" "$4"
}

fatal() {
  echo "FATAL: $1" >&2
  exit 2
}

[ -f "$ASSERT" ] || fatal "缺少协议校验器 $ASSERT"
[ -f "$ROBUST_CHECK" ] || fatal "缺少鲁棒性校验器 $ROBUST_CHECK"
[ -d "$ROBUST" ] || fatal "缺少鲁棒性夹具目录 $ROBUST"
if [ "$STATIC_ONLY" != 1 ]; then
  command -v "$DOCKER" >/dev/null 2>&1 || [ -x "$DOCKER" ] || fatal "未找到 docker（${DOCKER}）"
  command -v "$PY" >/dev/null 2>&1 || [ -x "$PY" ] || fatal "未找到 python3（${PY}）"
  command -v "$SHELLCHECK" >/dev/null 2>&1 || [ -x "$SHELLCHECK" ] || fatal "未找到 shellcheck（${SHELLCHECK}）"
fi

image_ok() { "$DOCKER" image inspect "$1" >/dev/null 2>&1; }

docker_sh() { # img out cmd [docker-run-args...]
  _img=$1
  _out=$2
  _cmd=$3
  shift 3
  "$DOCKER" run --rm --entrypoint sh "$@" -v "$W:/w:ro" -v "$FIX:/f:ro" "$_img" -c "$_cmd" > "$_out" 2>"$_out.err"
}

run_assert() { # out slug [assert-args...] -> 失败时向 stdout 打印原因并返回 1
  _out=$1
  _slug=$2
  shift 2
  if "$PY" "$ASSERT" "$_out" "$_slug" "$@" > "$TMP/assert.log" 2>&1; then
    return 0
  fi
  head -1 "$TMP/assert.log"
  return 1
}

# spec 2.4 规定的能力入口；空串 = 该包默认形态不应声明 required_mcp
expected_mcp() {
  case "$1" in
    inspect-k8s-cluster) printf 'doops' ;;
    inspect-database) printf 'dosql' ;;
    *) printf '' ;;
  esac
}

check_static() { # slug -> 包目录/frontmatter/路径/字节/执行位/required_mcp（spec 2.1/2.2/2.4）
  slug=$1
  t0=$(now)
  dir="$W/$slug"
  md="$dir/SKILL.md"
  if [ ! -d "$dir" ]; then
    record FAIL "static:$slug" "0" "缺少包目录"
    return
  fi
  if [ ! -f "$md" ]; then
    record FAIL "static:$slug" "0" "缺少 SKILL.md"
    return
  fi
  if ! printf '%s' "$slug" | LC_ALL=C grep -Eq '^[a-z0-9][a-z0-9-]*$' || [ "${#slug}" -gt 63 ]; then
    record FAIL "static:$slug" "0" "slug 不匹配 ^[a-z0-9][a-z0-9-]{0,62}$"
    return
  fi
  fm="$TMP/fm-$slug"
  awk 'NR==1 && $0!="---" {exit} NR==1 {next} $0=="---" {exit} {print}' "$md" > "$fm"
  if [ ! -s "$fm" ]; then
    record FAIL "static:$slug" "0" "SKILL.md 无 frontmatter"
    return
  fi
  if ! awk '/^name:/ {if ($0 ~ /^name:[[:space:]]*[^[:space:]]/) ok=1} END {exit !ok}' "$fm"; then
    record FAIL "static:$slug" "0" "frontmatter 缺少 name"
    return
  fi
  if ! awk '/^description:/ {if ($0 ~ /^description:[[:space:]]*[^[:space:]]/) ok=1} END {exit !ok}' "$fm"; then
    record FAIL "static:$slug" "0" "frontmatter 缺少 description"
    return
  fi
  if ! awk '/^(name|description):/ {next} /(^|[[:space:]])[.][.](\/|[[:space:]]|$)|:[[:space:]]*\// {bad=1} END {exit bad}' "$fm"; then
    record FAIL "static:$slug" "0" "frontmatter 含绝对路径或 .."
    return
  fi
  want=$(expected_mcp "$slug")
  have=$(awk '$1=="required_mcp:" {v=$2; gsub(/"/, "", v); print v; exit}' "$fm")
  if [ -z "$want" ]; then
    if [ -n "$have" ]; then
      record FAIL "static:$slug" "0" "spec 2.4 未列 required_mcp，实际声明 $have"
      return
    fi
  elif [ "$have" != "$want" ]; then
    record FAIL "static:$slug" "0" "required_mcp 应为 $want，实际 ${have:-无}"
    return
  fi
  if [ "$slug" = inspect-ssh-host ]; then
    if ! grep -q 'managed_ssh_channel' "$md"; then
      record FAIL "static:$slug" "0" "SKILL.md 未声明受管通道退化分支 check id=managed_ssh_channel"
      return
    fi
    if ! grep -q 'doops_ssh' "$md"; then
      record FAIL "static:$slug" "0" "SKILL.md 未点名唯一受管 SSH 通道 doops_ssh"
      return
    fi
  fi
  files=$(find "$dir" -type f | wc -l | tr -d ' ')
  att=$((files - 1))
  if [ "$att" -gt 16 ]; then
    record FAIL "static:$slug" "0" "附属文件 $att 个 > 16"
    return
  fi
  bytes=$(find "$dir" -type f -exec cat {} + | wc -c | tr -d ' ')
  if [ "$bytes" -gt 1048576 ]; then
    record FAIL "static:$slug" "0" "总字节 $bytes > 1 MiB"
    return
  fi
  if [ -d "$dir/scripts" ]; then
    for s in "$dir"/scripts/*.sh; do
      [ -e "$s" ] || continue
      if [ ! -x "$s" ]; then
        record FAIL "static:$slug" "0" "脚本不可执行: ${s##*/}"
        return
      fi
    done
  fi
  record PASS "static:$slug" "$(($(now) - t0))" ""
}

check_pkg() {
  slug=$1
  t0=$(now)
  script="$W/$slug/scripts/inspect.sh"
  if [ ! -f "$script" ]; then
    record FAIL "$slug" "$(($(now) - t0))" "缺少 scripts/inspect.sh"
    return
  fi
  if ! sh -n "$script" 2>"$TMP/syn"; then
    record FAIL "$slug" "$(($(now) - t0))" "sh -n 失败: $(head -1 "$TMP/syn")"
    return
  fi
  if ! "$SHELLCHECK" -s sh "$script" > "$TMP/sc" 2>&1; then
    record FAIL "$slug" "$(($(now) - t0))" "shellcheck: $(head -1 "$TMP/sc")"
    return
  fi
  if [ "$slug" = inspect-proxy-evasion ]; then
    pports=$(sed -n 's/^PROXY_PORTS = //p' "$script" | head -1)
    pwanted='{"1080", "3128", "7890", "7891", "8118", "10808", "20170"}'
    if [ "$pports" != "$pwanted" ]; then
      record FAIL "$slug" "$(($(now) - t0))" "PROXY_PORTS 未与 spec 2.4 端口表严格一致: $pports"
      return
    fi
  fi
  if ! image_ok "$IMG_PY"; then
    record FAIL "$slug" "$(($(now) - t0))" "缺少镜像 ${IMG_PY}（不静默跳过）"
    return
  fi

  out="$TMP/$slug.py.md"
  if ! docker_sh "$IMG_PY" "$out" "sh /w/$slug/scripts/inspect.sh"; then
    record FAIL "$slug" "$(($(now) - t0))" "python3 正路径退出码非 0: $(tail -1 "$out.err" 2>/dev/null)"
    return
  fi
  extra="--require-target"
  if [ "$slug" = inspect-vm-host ]; then
    extra="--require-target --require-metric disk_total_bytes --require-metric disk_used_bytes --require-metric disk_percent"
  fi
  # shellcheck disable=SC2086
  if ! reason=$(run_assert "$out" "$slug" $extra); then
    record FAIL "$slug" "$(($(now) - t0))" "python3 正路径协议断言: $reason"
    return
  fi

  case "$slug" in
    inspect-vm-host|inspect-container-runtime|inspect-k8s-cluster)
      if ! image_ok "$IMG_SH"; then
        record FAIL "$slug" "$(($(now) - t0))" "缺少无 python3 镜像 $IMG_SH"
        return
      fi
      out2="$TMP/$slug.sh.md"
      if ! docker_sh "$IMG_SH" "$out2" "sh /w/$slug/scripts/inspect.sh"; then
        record FAIL "$slug" "$(($(now) - t0))" "无 python3 降级路径退出码非 0: $(tail -1 "$out2.err" 2>/dev/null)"
        return
      fi
      if ! reason=$(run_assert "$out2" "$slug" --expect-status warning --require-target); then
        record FAIL "$slug" "$(($(now) - t0))" "降级路径协议断言: $reason"
        return
      fi
      ;;
  esac

  if [ "$slug" = inspect-app-web ]; then
    out3="$TMP/appweb.cred.md"
    if ! docker_sh "$IMG_PY" "$out3" "sh /w/$slug/scripts/inspect.sh" -e "DOOPS_INSPECT_URLS=http://sb01user:$CANARY@127.0.0.1:9/health"; then
      record FAIL "$slug" "$(($(now) - t0))" "凭据脱敏场景退出码非 0"
      return
    fi
    if ! reason=$(run_assert "$out3" "$slug" --forbid-substring "$CANARY"); then
      record FAIL "$slug" "$(($(now) - t0))" "凭据脱敏失败: $reason"
      return
    fi
  fi

  record PASS "$slug" "$(($(now) - t0))" ""
}

check_fixture() { # name file marker [docker-run-args...]
  name=$1
  file=$2
  marker=$3
  shift 3
  t0=$(now)
  if [ ! -f "$FIX/$file" ]; then
    record FAIL "fixture:$name" "0" "缺少 $FIX/$file"
    return
  fi
  if ! sh -n "$FIX/$file" 2>"$TMP/fsyn"; then
    record FAIL "fixture:$name" "$(($(now) - t0))" "sh -n 失败: $(head -1 "$TMP/fsyn")"
    return
  fi
  if ! "$SHELLCHECK" -s sh "$FIX/$file" > "$TMP/fsc" 2>&1; then
    record FAIL "fixture:$name" "$(($(now) - t0))" "shellcheck: $(head -1 "$TMP/fsc")"
    return
  fi
  if ! image_ok "$IMG_PY"; then
    record FAIL "fixture:$name" "$(($(now) - t0))" "缺少镜像 ${IMG_PY}（不静默跳过）"
    return
  fi
  out="$TMP/fix-$name.log"
  if ! docker_sh "$IMG_PY" "$out" "sh /f/$file" "$@"; then
    record FAIL "fixture:$name" "$(($(now) - t0))" "夹具执行失败，详情 $out / $out.err"
    return
  fi
  if ! grep -q -- "$marker" "$out"; then
    record FAIL "fixture:$name" "$(($(now) - t0))" "缺少断言标记 $marker"
    return
  fi
  record PASS "fixture:$name" "$(($(now) - t0))" ""
}

check_robust() { # name file spec [docker-run-args...]
  name=$1
  rfile=$2
  rspec=$3
  shift 3
  t0=$(now)
  for f in "$ROBUST/_lib.sh" "$ROBUST/$rfile" "$ROBUST_CHECK"; do
    [ -f "$f" ] || { record FAIL "robust:$name" "0" "缺少 $f"; return; }
  done
  for f in "$ROBUST/_lib.sh" "$ROBUST/$rfile"; do
    if ! sh -n "$f" 2>"$TMP/rsyn"; then
      record FAIL "robust:$name" "$(($(now) - t0))" "sh -n 失败 ${f##*/}: $(head -1 "$TMP/rsyn")"
      return
    fi
    if ! "$SHELLCHECK" -s sh "$f" > "$TMP/rsc" 2>&1; then
      record FAIL "robust:$name" "$(($(now) - t0))" "shellcheck ${f##*/}: $(head -1 "$TMP/rsc")"
      return
    fi
  done
  if ! image_ok "$IMG_PY"; then
    record FAIL "robust:$name" "$(($(now) - t0))" "缺少镜像 ${IMG_PY}（不静默跳过）"
    return
  fi
  rdir="$TMP/robust-$name"
  mkdir -p "$rdir"
  log="$TMP/robust-$name.log"
  if ! "$DOCKER" run --rm --entrypoint sh "$@" \
      -v "$W:/w:ro" -v "$FIX:/f:ro" -v "$rdir:/out" \
      -e "CASE=$name" -e "OUT=/out" -e "W=/w" \
      "$IMG_PY" -c "sh /f/robust/$rfile" > "$log" 2>&1; then
    record FAIL "robust:$name" "$(($(now) - t0))" "夹具执行失败: $(tail -1 "$log")"
    return
  fi
  if ! grep -q -- "ROBUST_OK $name" "$log"; then
    record FAIL "robust:$name" "$(($(now) - t0))" "缺少 ROBUST_OK 标记: $(tail -1 "$log")"
    return
  fi
  printf '%s\n' "$rspec" | tr ';' '\n' > "$TMP/rspec-$name"
  summary=""
  while IFS= read -r item; do
    item=$(printf '%s' "$item" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [ -n "$item" ] || continue
    rslug=${item%%:*}
    ropts=""
    case "$item" in
      *:*) ropts=${item#*:} ;;
    esac
    rout="$rdir/$name.$rslug.md"
    if [ ! -f "$rout" ]; then
      record FAIL "robust:$name" "$(($(now) - t0))" "缺少产物 $rslug"
      return
    fi
    if ! rline=$("$PY" "$ROBUST_CHECK" "$rout" "$rslug" --spec "$ropts" 2>&1); then
      record FAIL "robust:$name" "$(($(now) - t0))" "$rline"
      return
    fi
    summary="${summary}${rline#ROBUST_CHECK }; "
  done < "$TMP/rspec-$name"
  record PASS "robust:$name" "$(($(now) - t0))" "$summary"
}

echo "SB-01 校验开始：python3 镜像=${IMG_PY}；无 python3 镜像=${IMG_SH}"
echo "工作区 W=$W"
echo ""
if [ "$ROBUST_ONLY" != 1 ]; then
  echo "-- 包静态契约（spec 2.1 / 2.2 / 2.4；全部 8 包） --"
  for pkg in $ALL_PKGS; do
    check_static "$pkg"
  done
else
  echo "（SB01_ROBUST_ONLY=1：只跑 E 鲁棒性矩阵）"
fi

if [ "$STATIC_ONLY" = 1 ]; then
  echo ""
  echo "（SB01_STATIC_ONLY=1：跳过 docker 动态检查）"
else
  if [ "$ROBUST_ONLY" != 1 ]; then
    echo ""
    echo "-- 脚本包 --"
    for slug in $SLUGS; do
      check_pkg "$slug"
    done

    echo ""
    echo "-- 红线 2 去重夹具 --"
    check_fixture dedup_mount dedup_vmhost.sh DEDUP_MOUNT_OK --privileged
    check_fixture dedup_overlay_shim dedup_overlay_shim.sh DEDUP_OVERLAY_OK
    check_fixture dedup_overlay_lowerdir dedup_overlay_lowerdir.sh DEDUP_LOWERDIR_OK --privileged
    check_fixture dedup_virtual_approx dedup_virtual_approx_shim.sh DEDUP_APPROX_OK

    echo ""
    echo "-- 运行时正常/异常路径夹具 --"
    check_fixture runtime_stats runtime_stats_shim.sh RUNTIME_STATS_OK
    check_fixture runtime_stats_fail runtime_stats_fail_shim.sh RUNTIME_STATS_FAIL_OK
  fi

  echo ""
  echo "-- SF-08 鲁棒性矩阵（畸形环境真跑；每行给出是否降级与理由） --"
  check_robust longline case_longline.sh 'inspect-vm-host:max=262144'
  check_robust nonutf8 case_nonutf8.sh 'inspect-vm-host:minmounts=1,forbid=未采集到可去重'
  check_robust locale_c case_locale_c.sh "$ROBUST_ALL_PKGS"
  check_robust locale_ja case_locale_ja.sh "$ROBUST_ALL_PKGS"
  check_robust tz case_tz.sh 'inspect-vm-host;inspect-k8s-cluster'
  check_robust no_df case_no_df.sh 'inspect-vm-host:status=warning,degraded=1,check=host.disk'
  check_robust no_free case_no_free.sh "$ROBUST_ALL_PKGS"
  check_robust no_awk case_no_awk.sh "$ROBUST_ALL_PKGS"
  check_robust no_python3 case_no_python3.sh "$ROBUST_ALL_FALLBACK"
  check_robust proc_meminfo case_proc_meminfo.sh 'inspect-vm-host:status=warning,degraded=1' --privileged
  check_robust proc_loadavg case_proc_loadavg.sh 'inspect-vm-host:degraded=1' --privileged
  check_robust mounts_symlink case_mounts_symlink.sh 'inspect-vm-host:mount=/tmp/lp/a'
  check_robust mounts_space case_mounts_space.sh 'inspect-vm-host:mount=/mnt/我的 数据'
  check_robust mounts_many case_mounts_many.sh 'inspect-vm-host:check=output.size'
fi

echo ""
echo "================ SB-01 校验汇总 ================"
printf '%-30s %-6s %-9s %s\n' "包 / 夹具" "结果" "耗时(s)" "失败原因"
while IFS='|' read -r name st secs reason; do
  printf '%-30s %-6s %-9s %s\n' "$name" "$st" "$secs" "$reason"
done < "$ROWS"
echo "------------------------------------------------"
printf '合计 %d 项：通过 %d，失败 %d\n' "$TOTAL" "$PASSED" "$FAILED"
if [ "$FAILED" -gt 0 ]; then
  exit 1
fi
echo "SB-01 VERIFY_ALL_PASS"
