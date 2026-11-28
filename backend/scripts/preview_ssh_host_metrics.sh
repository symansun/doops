#!/usr/bin/env bash
# Single read-only observation. No upload, password input, timer or remote install.
set -euo pipefail
usage() { echo "Usage: bash $0 ROUTE USER@HOST SSH_PORT CLUSTER/INSTANCE ASSET_ID [MOUNT ...]" >&2; exit 2; }
(( $# >= 5 )) || usage
route=$1 dest=$2 port=$3 source_id=$4 asset=$5; shift 5
[[ $port =~ ^[1-9][0-9]{0,4}$ ]] && (( 10#$port <= 65535 )) || usage
[[ $source_id =~ ^[^/[:space:]]+/[^/[:space:]]+$ && $dest == *@* && $dest != -* && -n $asset && -n $route ]] || usage
(( $# )) || set -- /
mounts=(); for mount in "$@"; do [[ $mount == /* ]] || usage; mounts+=(--mount "$mount"); done

cli="$HOME/.local/bin/doops"
session="host-sample-$$"
# Pass the reviewed collector as ONE remote argv; do not install it on the host.
collector=$(< "$(dirname -- "$0")/asset_monitor_collector.py")
"$cli" -session "$session" capabilities --target "$route" --require structuredSSH >&2
exec "$cli" -session "$session" ssh --target "$route" --host "$dest" \
  --option="Port=$port" --option=BatchMode=yes --option=StrictHostKeyChecking=yes --option=ConnectTimeout=8 \
  -- python3 -I -B -c "$collector" --type machine --host --asset-id "$asset" \
     --cluster "${source_id%/*}" --instance "${source_id#*/}" "${mounts[@]}"
