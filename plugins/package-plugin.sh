#!/usr/bin/env bash
set -euo pipefail

# Package a standalone DoOps plugin directory (e.g. plugins/dodns or plugins/doemail)
# into a clean .zip ready to upload in DoOps Plugin Center ("安装插件包 (.zip)").
# Excludes unit tests and hidden files so the bundle stays minimal and <= 1 MiB.

if [ $# -lt 1 ]; then
  echo "Usage: $0 <plugin-dir> [output-zip]" >&2
  exit 1
fi

SRC_DIR="$(cd "$1" && pwd)"
SLUG="$(basename "$SRC_DIR")"
OUT_ZIP="${2:-$(pwd)/${SLUG}-plugin.zip}"

if [ ! -f "$SRC_DIR/SKILL.md" ] || [ ! -f "$SRC_DIR/plugin.yaml" ]; then
  echo "Error: $SRC_DIR must contain both SKILL.md and plugin.yaml" >&2
  exit 1
fi

rm -f "$OUT_ZIP"
python3 - "$SRC_DIR" "$OUT_ZIP" <<'PY'
import os, sys, zipfile

src_dir, out_zip = sys.argv[1], sys.argv[2]
count = 0
with zipfile.ZipFile(out_zip, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    for root, dirs, files in os.walk(src_dir):
        dirs[:] = sorted(d for d in dirs if not d.startswith(".") and d != "__pycache__" and d != "tests")
        for fn in sorted(files):
            if fn.startswith(".") or fn.endswith("_test.py") or fn.endswith(".test.mjs") or fn.endswith(".pyc"):
                continue
            full = os.path.join(root, fn)
            rel = os.path.relpath(full, src_dir)
            zf.write(full, rel)
            count += 1
print(f"Packaged {count} files -> {out_zip}")
PY
