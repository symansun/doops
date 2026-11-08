#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
First installation on an empty Ubuntu/Debian Docker host (run as root).
Installs Docker Engine and Compose if absent; never replaces an existing Agent.

  bash backend/scripts/bootstrap-host-agent.sh \
    --version vX.Y.Z --gateway https://gateway.example.com \
    --cluster doops-unicom --instance unicom-1

Required: --version, --gateway, --cluster, --instance
The Agent receives its model provider from the Gateway after it connects;
no model settings or credentials are copied to the host.
No Gateway operator token is required for Agent registration.
Files: /opt/doops-agent/compose.yaml
No host ports or host Docker socket are exposed to the container.
EOF
}

RELEASE_VERSION="" GATEWAY="" CLUSTER="" INSTANCE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version|--gateway|--cluster|--instance)
      [[ $# -ge 2 && -n "$2" ]] || { usage >&2; exit 2; }
      case "$1" in
        --version) RELEASE_VERSION="$2" ;;
        --gateway) GATEWAY="$2" ;;
        --cluster) CLUSTER="$2" ;;
        --instance) INSTANCE="$2" ;;
      esac
      shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
[[ "$RELEASE_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Explicit --version vX.Y.Z required" >&2; exit 2; }
[[ "$GATEWAY" =~ ^https://[a-zA-Z0-9.-]+(:[0-9]+)?/?$ ]] || { echo "HTTPS Gateway origin required" >&2; exit 2; }
for identity in "$CLUSTER" "$INSTANCE"; do
  [[ "$identity" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$ ]] || { echo "Explicit cluster and instance required" >&2; exit 2; }
done
[[ "$(id -u)" == 0 ]] || { echo "Run with sudo or as root" >&2; exit 1; }
exec 9>/run/lock/doops-agent-bootstrap.lock
flock -n 9 || { echo "Another Agent installation is running" >&2; exit 1; }
[[ ! -e /opt/doops-agent ]] || { echo "/opt/doops-agent already exists; use the upgrade flow" >&2; exit 1; }
. /etc/os-release
[[ "$ID" == ubuntu || "$ID" == debian ]] || { echo "Supported hosts: Ubuntu or Debian" >&2; exit 1; }
case "$(uname -m)" in
  x86_64) PLATFORM=linux/amd64 ;;
  aarch64|arm64) PLATFORM=linux/arm64 ;;
  *) echo "Unsupported architecture" >&2; exit 1 ;;
esac
if command -v docker >/dev/null; then
  docker info >/dev/null
  docker compose version >/dev/null
  [[ -z "$(docker ps -aq --filter 'name=^/doops-agent$')" ]] || { echo "Agent container already exists" >&2; exit 1; }
fi

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl python3
if ! command -v docker >/dev/null; then
  install -d -m 0755 /etc/apt/keyrings
  curl --fail --silent --show-error --location \
    "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc
  chmod 0644 /etc/apt/keyrings/docker.asc
  python3 - "$ID" "${VERSION_CODENAME:?OS codename required}" "$(dpkg --print-architecture)" <<'PY'
import pathlib
import sys
os_id, codename, arch = sys.argv[1:]
path = pathlib.Path("/etc/apt/sources.list.d/docker.sources")
expected = (
    f"Types: deb\nURIs: https://download.docker.com/linux/{os_id}\n"
    f"Suites: {codename}\nComponents: stable\nArchitectures: {arch}\n"
    "Signed-By: /etc/apt/keyrings/docker.asc\n"
)
if path.exists() and path.read_text() != expected:
    raise SystemExit("Conflicting Docker repository configuration; refusing to overwrite")
path.write_text(expected)
PY
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y \
    docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi
systemctl enable --now docker
docker info >/dev/null
docker compose version >/dev/null
REPOSITORY=ghcr.io/symansun/doops-agent
docker pull --platform "$PLATFORM" "$REPOSITORY:$RELEASE_VERSION"
IMAGE="$(docker image inspect --format '{{index .RepoDigests 0}}' "$REPOSITORY:$RELEASE_VERSION")"
[[ "$IMAGE" =~ ^ghcr.io/symansun/doops-agent@sha256:[a-f0-9]{64}$ ]] || { echo "Registry digest missing" >&2; exit 1; }
IDENTITY="$(docker run --rm --network none --platform "$PLATFORM" --entrypoint /app/doops-agent "$IMAGE" -version)"
[[ "$IDENTITY" =~ ^release=${RELEASE_VERSION}[[:space:]]commit=[a-f0-9]{40}$ ]] || { echo "Image release identity mismatch" >&2; exit 1; }

install -d -m 0700 /opt/doops-agent
python3 - "$IMAGE" "$PLATFORM" "$GATEWAY" "$CLUSTER" "$INSTANCE" <<'PY'
import json
import pathlib
import sys

image, platform, gateway, cluster, instance = sys.argv[1:]
config = {
    "name": "doops-agent",
    "services": {
        "agent": {
            "image": image,
            "platform": platform,
            "container_name": "doops-agent",
            "restart": "unless-stopped",
            "privileged": True,
            "command": ["-listen", "0.0.0.0", "-gateway-url", gateway,
                        "-cluster", cluster, "-instance", instance],
            "volumes": [
                "workspace:/root/ws",
                "agent-state:/root/.agent",
            ],
            "healthcheck": {
                "test": ["CMD", "curl", "-fsS", "--max-time", "3",
                         "http://127.0.0.1:42222/health/ready"],
                "interval": "5s", "timeout": "4s", "retries": 36,
                "start_period": "20s",
            },
            "logging": {"driver": "json-file",
                        "options": {"max-size": "10m", "max-file": "3"}},
        }
    },
    "volumes": {"workspace": {}, "agent-state": {}},
}
path = pathlib.Path("/opt/doops-agent/compose.yaml")
path.write_text(json.dumps(config, indent=2) + "\n")
path.chmod(0o600)
PY
docker compose -f /opt/doops-agent/compose.yaml config --quiet
if ! docker compose -f /opt/doops-agent/compose.yaml up -d --wait --wait-timeout 240 ||
  [[ "$(docker inspect --format '{{.State.Health.Status}}' doops-agent)" != healthy ]] ||
  [[ "$(docker exec doops-agent /app/doops-agent -version)" != "$IDENTITY" ]]; then
  docker compose -f /opt/doops-agent/compose.yaml stop
  echo "Agent did not become ready; stopped container, configuration retained in /opt/doops-agent" >&2
  exit 1
fi
printf 'Agent registered: %s/%s\n%s\n' "$CLUSTER" "$INSTANCE" "$IDENTITY"
echo "Gateway tunnel ready. Operator RPC and AI acceptance must still be run from the management CLI."
