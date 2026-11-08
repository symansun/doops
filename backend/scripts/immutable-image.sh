#!/usr/bin/env bash
# Returns 0 for an existing verified source, 1 only for a missing manifest, and
# 2 for a conflicting tag or an unavailable registry. Never overwrites a tag.
doops_existing_release_image() {
  local image="$1" source_commit="$2" release_tag="$3" temporary result
  temporary="$(mktemp -d)"
  if skopeo inspect "docker://${image}" >"${temporary}/image.json" 2>"${temporary}/error"; then
    if ! python3 - "${temporary}/image.json" "${source_commit}" "${release_tag}" <<'PY'
import json, sys
image = json.load(open(sys.argv[1]))
labels = image.get("Labels") or {}
if labels.get("org.opencontainers.image.revision") != sys.argv[2] or labels.get("org.opencontainers.image.version") != sys.argv[3]:
    raise SystemExit("immutable image tag belongs to another release source; refusing overwrite")
if not __import__('re').fullmatch(r"sha256:[0-9a-f]{64}", image.get("Digest", "")):
    raise SystemExit("existing image has no verified registry digest")
PY
    then
      rm -rf "${temporary}"
      return 2
    fi
    rm -rf "${temporary}"
    return 0
  fi
  if grep -Eqi 'manifest unknown|manifest_unknown|name unknown|name_unknown' "${temporary}/error" &&
     ! grep -Eqi 'unauthorized|denied|forbidden|timeout|timed out' "${temporary}/error"; then
    result=1
  else
    cat "${temporary}/error" >&2
    result=2
  fi
  rm -rf "${temporary}"
  return "${result}"
}
