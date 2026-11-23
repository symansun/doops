#!/bin/sh
# SF-08 鲁棒性夹具公共库（POSIX sh；由夹具以 . /f/robust/_lib.sh 载入）
# 约定：W=skills_builtin 只读挂载点，OUT=可写产物目录，CASE=场景名，SHIM=命令替身目录。
set -u

W=${W:-/w}
OUT=${OUT:-/out}
CASE=${CASE:-case}
SHIM=/shim
PKGS="inspect-vm-host inspect-container-runtime inspect-k8s-cluster inspect-app-web inspect-package-vuln inspect-proxy-evasion"

mkdir -p "$OUT" "$SHIM" 2>/dev/null || true

make_shim() { # make_shim <cmd>：替身脚本体从 stdin 读取
  _name=$1
  _file="$SHIM/$_name"
  {
    printf '#!/bin/sh\n'
    cat
  } > "$_file"
  chmod +x "$_file"
}

require_shim_path() {
  PATH="$SHIM:$PATH"
  export PATH
}

path_without() { # path_without <cmd>：构造只缺该命令的 PATH（真正移除，而非替换或报错）
  _skip=$1
  rm -rf /nopath
  mkdir -p /nopath
  for _d in /bin /usr/bin /sbin /usr/sbin /usr/local/bin /usr/local/sbin; do
    [ -d "$_d" ] || continue
    for _f in "$_d"/*; do
      [ -e "$_f" ] || continue
      _b=${_f##*/}
      [ "$_b" = "$_skip" ] && continue
      [ -e "/nopath/$_b" ] && continue
      ln -s "$_f" "/nopath/$_b" 2>/dev/null || true
    done
  done
  # 有意替换 PATH 本身，只为把单个命令从搜索路径中移除
  # shellcheck disable=SC2123
  PATH=/nopath
  export PATH
}

run_pkg() { # run_pkg <slug> [env-assignment...]
  _slug=$1
  shift
  _out="$OUT/$CASE.$_slug.md"
  if ! env "$@" sh "$W/$_slug/scripts/inspect.sh" > "$_out" 2> "$OUT/$CASE.$_slug.err"; then
    echo "ROBUST_FAIL $CASE $_slug rc-nonzero"
    exit 1
  fi
  if [ ! -s "$_out" ]; then
    echo "ROBUST_FAIL $CASE $_slug empty-output"
    exit 1
  fi
}

all_pkgs() { # all_pkgs [env-assignment...]
  for _s in $PKGS; do
    run_pkg "$_s" "$@"
  done
}

ok() {
  echo "ROBUST_OK $CASE"
}
