#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d /tmp/doops-proxy-smoke.XXXXXX)"
GW_BIN="${TMP}/doops-gateway"
AGENT_BIN="${TMP}/doops-agent"
GW_DB="${TMP}/gateway.db"
GW_LOG="${TMP}/gateway.log"
AGENT_LOG="${TMP}/agent.log"
UPSTREAM_LOG="${TMP}/upstream.log"

read -r GW_PORT AGENT_PORT UPSTREAM_PORT < <(
  python3 - <<'PY'
import socket

ports = []
for _ in range(3):
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    ports.append(sock.getsockname()[1])
    sock.close()
print(*ports)
PY
)

GW_PID=""
AGENT_PID=""
UPSTREAM_PID=""

cleanup() {
  set +e
  [[ -n "${AGENT_PID}" ]] && kill "${AGENT_PID}" 2>/dev/null
  [[ -n "${GW_PID}" ]] && kill "${GW_PID}" 2>/dev/null
  [[ -n "${UPSTREAM_PID}" ]] && kill "${UPSTREAM_PID}" 2>/dev/null
  wait 2>/dev/null
  rm -rf "${TMP}"
}
trap cleanup EXIT INT TERM

wait_for_http() {
  local url="$1"
  local expected="$2"
  for _ in $(seq 1 100); do
    if curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' "${url}" 2>/dev/null |
      grep -Fxq "${expected}"; then
      return 0
    fi
    sleep 0.1
  done
  echo "timed out waiting for ${url} (expected HTTP ${expected})" >&2
  return 1
}

echo "[1/8] building gateway and agent"
(cd "${ROOT}/gateway" && go build -o "${GW_BIN}" .)
(cd "${ROOT}/agent" && go build -o "${AGENT_BIN}" ./cmd/agent)

echo "[2/8] starting the private upstream site on 127.0.0.1:${UPSTREAM_PORT}"
python3 -u - "${UPSTREAM_PORT}" >"${UPSTREAM_LOG}" 2>&1 <<'PY' &
import http.server
import sys

port = int(sys.argv[1])

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = f"local-site path={self.path} host={self.headers.get('Host')}".encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("X-Upstream", "local-agent-network")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = b"post-body=" + self.rfile.read(length)
        self.send_response(201)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        print(fmt % args, flush=True)

http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
UPSTREAM_PID=$!
wait_for_http "http://127.0.0.1:${UPSTREAM_PORT}/health" 200

echo "[3/8] starting the real gateway process"
"${GW_BIN}" serve \
  -db "${GW_DB}" \
  -port "${GW_PORT}" \
  -operation-timeout 10s \
  -target-queue-timeout 2s >"${GW_LOG}" 2>&1 &
GW_PID=$!
wait_for_http "http://127.0.0.1:${GW_PORT}/health" 200

echo "[4/8] creating a synthetic admin route"
"${GW_BIN}" user create -db "${GW_DB}" -name smoke-admin >/dev/null
"${GW_BIN}" grant -db "${GW_DB}" -user smoke-admin -admin >/dev/null
TOKEN="$("${GW_BIN}" token create -db "${GW_DB}" -user smoke-admin -name smoke |
  awk -F= '/^token=/{print $2}')"
test -n "${TOKEN}"
curl --noproxy '*' -fsS -X POST \
  "http://127.0.0.1:${GW_PORT}/v1/admin/proxy-routes" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  --data "{\"host\":\"site.local.test\",\"cluster\":\"dev\",\"instance\":\"local\",\"upstream\":\"http://127.0.0.1:${UPSTREAM_PORT}\",\"enabled\":true}" \
  >"${TMP}/route.json"
grep -Fq '"host":"site.local.test"' "${TMP}/route.json"

echo "[5/8] starting the real agent with an outbound-only tunnel"
DOOPS_PROXY_ALLOW_HOSTS=127.0.0.1 "${AGENT_BIN}" \
  -gateway-url "http://127.0.0.1:${GW_PORT}" \
  -cluster dev \
  -instance local \
  -listen 127.0.0.1 \
  -port "${AGENT_PORT}" \
  -reconnect-delay 100ms >"${AGENT_LOG}" 2>&1 &
AGENT_PID=$!
wait_for_http "http://127.0.0.1:${AGENT_PORT}/health/ready" 200

echo "[6/8] verifying GET Host routing, query, response header and body"
curl --noproxy '*' -fsS -D "${TMP}/headers" \
  -H 'Host: site.local.test' \
  "http://127.0.0.1:${GW_PORT}/demo/page?from=smoke" >"${TMP}/get-body"
expected_get_body="local-site path=/demo/page?from=smoke host=127.0.0.1:${UPSTREAM_PORT}"
[[ "$(cat "${TMP}/get-body")" == "${expected_get_body}" ]]
tr -d '\r' <"${TMP}/headers" >"${TMP}/headers.normalized"
grep -Fxq 'X-Upstream: local-agent-network' "${TMP}/headers.normalized"

echo "[7/8] verifying POST body forwarding"
curl --noproxy '*' -fsS -o "${TMP}/post-body" \
  -w 'post_status=%{http_code}\n' \
  -X POST \
  -H 'Host: site.local.test' \
  --data 'through-agent' \
  "http://127.0.0.1:${GW_PORT}/forms" |
  grep -Fxq 'post_status=201'
[[ "$(cat "${TMP}/post-body")" == 'post-body=through-agent' ]]

echo "[8/8] smoke result"
printf 'gateway_health='
curl --noproxy '*' -fsS "http://127.0.0.1:${GW_PORT}/health"
printf '\nagent_ready='
curl --noproxy '*' -fsS "http://127.0.0.1:${AGENT_PORT}/health/ready"
printf '\nproxy_tunnel_smoke=PASS\n'
