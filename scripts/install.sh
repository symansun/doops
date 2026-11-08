#!/usr/bin/env bash
set -euo pipefail

version=""
args=("$@")
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) version="${2:-}"; shift 2 ;;
    -h|--help)
      echo 'Usage: bash install.sh --version vMAJOR.MINOR.PATCH --project /absolute/project [target options]'
      echo 'Downloads the matching versioned installer from github.com/symansun/doops.'
      exit 0 ;;
    *) shift ;;
  esac
done
if [[ ! "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo '--version vMAJOR.MINOR.PATCH is required' >&2
  exit 2
fi
temporary="$(mktemp -d)"
trap 'rm -rf "${temporary}"' EXIT
curl --fail --location --silent --show-error \
  --output "${temporary}/install.sh" \
  "https://raw.githubusercontent.com/symansun/doops/${version}/backend/scripts/install.sh"
bash "${temporary}/install.sh" "${args[@]}"
