"""Zero-mock cross-module loopback E2E gate: real CLI <-> real Gateway <-> real Agent.

Compiles the real `doops-gateway`, `doops-agent`, and `doops` CLI binaries once per
module, boots an isolated loopback Gateway (temporary SQLite DB + synthetic master key)
and an isolated Agent (temporary `DOOPS_WORKSPACE_ROOT`, connected over the real
WebSocket reverse tunnel), and verifies end-to-end protocol contracts with zero mocks:

1. `doops push --storage gateway` (--dry-run, initial push, incremental overwrite/delete,
   and exact on-disk `.doops-ready` commit SHA verification).
2. `doops exec --raw` / `--quiet` pure JSON stdout (zero `[TARGETING]`, `Operation
   complete.`, ANSI escapes, or heartbeat contamination; errors routed to stderr).
3. `doops pull --storage gateway` round-trip after remote workspace edits.
4. `doops clean` session contract (reuses explicit `-session` when `--workspace` is
   omitted; fails closed when neither `-session` nor `--workspace` is provided).
5. Real TCP/HTTP/WebSocket reverse proxy injecting `502` + `503` transient handshake
   failures (transparent retry to real Gateway) and `401` (strictly 1 attempt, 0 retries).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from http import HTTPStatus
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request

import pytest


ROOT = Path(__file__).resolve().parents[2]
CLUSTER = "e2e-cluster"
INSTANCE = "e2e-agent-1"
TARGET_NAME = "e2e-target"
SHA40_RE = re.compile(r"^[0-9a-f]{40}$")
PROXY_ENV_KEYS = (
    "HTTP_PROXY",
    "HTTPS_PROXY",
    "ALL_PROXY",
    "http_proxy",
    "https_proxy",
    "all_proxy",
)


def _go_build_env() -> dict[str, str]:
    """Return an environment for `go build` resilient to a stale host GOROOT."""
    env = os.environ.copy()
    # Unsetting GOROOT lets the `go` binary on PATH resolve its own toolchain root
    # (needed when a host shell exports an older GOROOT; a no-op under CI setup-go).
    env.pop("GOROOT", None)
    return env


def _clean_loopback_env(*, drop_prefixes: tuple[str, ...] = ()) -> dict[str, str]:
    env = os.environ.copy()
    for key in PROXY_ENV_KEYS:
        env.pop(key, None)
    for key in list(env):
        if any(key.startswith(prefix) for prefix in drop_prefixes):
            env.pop(key, None)
    env["NO_PROXY"] = "127.0.0.1,localhost"
    env["no_proxy"] = "127.0.0.1,localhost"
    return env


def _free_tcp_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def _http_get(url: str, headers: dict[str, str] | None = None, timeout: float = 2.0) -> tuple[int, bytes]:
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    req = urllib.request.Request(url, headers=headers or {}, method="GET")
    try:
        with opener.open(req, timeout=timeout) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read()


def _read_tail(path: Path, max_bytes: int = 8192) -> str:
    if not path.is_file():
        return "<log file absent>"
    data = path.read_bytes()
    if len(data) > max_bytes:
        data = data[-max_bytes:]
    return data.decode("utf-8", errors="replace")


def _terminate_process(proc: subprocess.Popen[bytes] | None) -> None:
    if proc is None or proc.poll() is not None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=5)


def _unique_session(prefix: str) -> str:
    return f"{prefix}-{secrets.token_hex(4)}"


@dataclass
class BuiltBinaries:
    gateway: Path
    agent: Path
    cli: Path


@dataclass
class LiveE2EStack:
    binaries: BuiltBinaries
    run_dir: Path
    workspace_root: Path
    gateway_port: int
    agent_health_port: int
    token: str
    config_path: Path
    gateway_log: Path
    agent_log: Path
    tracked_sessions: list[str] = field(default_factory=list)

    @property
    def gateway_url(self) -> str:
        return f"http://127.0.0.1:{self.gateway_port}"

    def track_session(self, session_id: str) -> str:
        self.tracked_sessions.append(session_id)
        return session_id

    def cli_env(self, config_path: Path | None = None) -> dict[str, str]:
        env = _clean_loopback_env(drop_prefixes=("DOOPS_",))
        env["DOOPS_CONFIG"] = str(config_path or self.config_path)
        return env

    def run_cli(
        self,
        args: list[str],
        *,
        config_path: Path | None = None,
        stdin_text: str | None = None,
        timeout: float = 45.0,
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [str(self.binaries.cli), *args],
            env=self.cli_env(config_path),
            input=stdin_text,
            capture_output=True,
            text=True,
            timeout=timeout,
        )

    def diag(self) -> str:
        return (
            f"\n--- gateway.log ---\n{_read_tail(self.gateway_log)}"
            f"\n--- agent.log ---\n{_read_tail(self.agent_log)}"
        )


class _FaultInjectingReverseProxy:
    """Minimal pure-stdlib TCP/HTTP reverse proxy supporting WebSocket Upgrade passthrough.

    Intercepts requests whose path is `/v1/rpc` and replies with the scripted HTTP fault
    statuses in sequence (or `default_rpc_fault` once exhausted); all other requests and
    non-fault `/v1/rpc` connections are transparently tunneled byte-for-byte (including
    `101 Switching Protocols` WebSocket upgrades) to the real loopback Gateway.
    """

    def __init__(
        self,
        upstream_port: int,
        rpc_faults: list[int] | None = None,
        default_rpc_fault: int | None = None,
    ) -> None:
        self.upstream_port = upstream_port
        self._rpc_faults = list(rpc_faults or [])
        self._default_rpc_fault = default_rpc_fault
        self._lock = threading.Lock()
        self.rpc_attempts: list[int | str] = []

        self._stop = threading.Event()
        self._active_sockets: set[socket.socket] = set()
        self._threads: list[threading.Thread] = []

        self._server_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self._server_sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self._server_sock.bind(("127.0.0.1", 0))
        self._server_sock.listen(32)
        self._server_sock.settimeout(0.2)
        self.port = int(self._server_sock.getsockname()[1])

        self._accept_thread = threading.Thread(
            target=self._accept_loop, name=f"e2e-proxy-{self.port}", daemon=True
        )
        self._accept_thread.start()

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    def _register_sock(self, sock: socket.socket) -> None:
        with self._lock:
            self._active_sockets.add(sock)

    def _unregister_sock(self, sock: socket.socket) -> None:
        with self._lock:
            self._active_sockets.discard(sock)

    def _next_rpc_action(self) -> int | None:
        with self._lock:
            if self._rpc_faults:
                status = self._rpc_faults.pop(0)
                self.rpc_attempts.append(status)
                return status
            if self._default_rpc_fault is not None:
                status = self._default_rpc_fault
                self.rpc_attempts.append(status)
                return status
            self.rpc_attempts.append("passthrough")
            return None

    def _accept_loop(self) -> None:
        while not self._stop.is_set():
            try:
                client_sock, _ = self._server_sock.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            self._register_sock(client_sock)
            thread = threading.Thread(
                target=self._handle_client, args=(client_sock,), daemon=True
            )
            with self._lock:
                self._threads.append(thread)
            thread.start()

    @staticmethod
    def _read_http_head(sock: socket.socket) -> tuple[bytes, bytes]:
        sock.settimeout(5.0)
        buf = bytearray()
        while b"\r\n\r\n" not in buf:
            chunk = sock.recv(4096)
            if not chunk:
                return bytes(buf), b""
            buf.extend(chunk)
            if len(buf) > 65536:
                raise ValueError("HTTP request header exceeded 64 KiB")
        head, tail = bytes(buf).split(b"\r\n\r\n", 1)
        return head, tail

    @staticmethod
    def _rewrite_non_upgrade_connection_close(head: bytes) -> bytes:
        lines = head.split(b"\r\n")
        if not lines:
            return head
        is_upgrade = any(
            line.lower().startswith(b"upgrade:") and b"websocket" in line.lower()
            for line in lines[1:]
        )
        if is_upgrade:
            return head
        filtered = [lines[0]]
        for line in lines[1:]:
            if line.lower().startswith(b"connection:"):
                continue
            filtered.append(line)
        filtered.append(b"Connection: close")
        return b"\r\n".join(filtered)

    @staticmethod
    def _pump(src: socket.socket, dst: socket.socket) -> None:
        try:
            while True:
                data = src.recv(16384)
                if not data:
                    break
                dst.sendall(data)
        except OSError:
            pass
        finally:
            try:
                dst.shutdown(socket.SHUT_WR)
            except OSError:
                pass

    def _handle_client(self, client_sock: socket.socket) -> None:
        upstream_sock: socket.socket | None = None
        try:
            head, tail = self._read_http_head(client_sock)
            if not head:
                return
            first_line = head.split(b"\r\n", 1)[0].decode("latin-1")
            parts = first_line.split(" ", 2)
            if len(parts) < 2:
                return
            path = parts[1].split("?", 1)[0]

            if path == "/v1/rpc":
                fault_status = self._next_rpc_action()
                if fault_status is not None:
                    reason = HTTPStatus(fault_status).phrase
                    body = f"injected reverse-proxy fault: {fault_status} {reason}\n".encode("utf-8")
                    response = (
                        f"HTTP/1.1 {fault_status} {reason}\r\n"
                        f"Content-Type: text/plain; charset=utf-8\r\n"
                        f"Content-Length: {len(body)}\r\n"
                        f"Connection: close\r\n"
                        f"\r\n"
                    ).encode("latin-1") + body
                    client_sock.sendall(response)
                    return

            upstream_sock = socket.create_connection(("127.0.0.1", self.upstream_port), timeout=5.0)
            self._register_sock(upstream_sock)
            client_sock.settimeout(None)
            upstream_sock.settimeout(None)

            forwarded_head = self._rewrite_non_upgrade_connection_close(head)
            upstream_sock.sendall(forwarded_head + b"\r\n\r\n" + tail)

            c2u = threading.Thread(
                target=self._pump, args=(client_sock, upstream_sock), daemon=True
            )
            c2u.start()
            self._pump(upstream_sock, client_sock)
            c2u.join(timeout=5.0)
        except OSError:
            pass
        finally:
            for sock in (client_sock, upstream_sock):
                if sock is not None:
                    self._unregister_sock(sock)
                    try:
                        sock.close()
                    except OSError:
                        pass

    def close(self) -> None:
        self._stop.set()
        try:
            self._server_sock.close()
        except OSError:
            pass
        with self._lock:
            sockets = list(self._active_sockets)
            threads = list(self._threads)
        for sock in sockets:
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            try:
                sock.close()
            except OSError:
                pass
        self._accept_thread.join(timeout=3.0)
        for thread in threads:
            thread.join(timeout=3.0)

    def __enter__(self) -> _FaultInjectingReverseProxy:
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()


@pytest.fixture(scope="module")
def built_binaries(tmp_path_factory: pytest.TempPathFactory) -> BuiltBinaries:
    bin_dir = tmp_path_factory.mktemp("doops-e2e-bins")
    env = _go_build_env()
    targets = (
        ("doops-gateway", ROOT / "backend" / "gateway", "."),
        ("doops-agent", ROOT / "backend" / "agent", "./cmd/agent"),
        ("doops", ROOT / "backend" / "cli", "./cli/doops"),
    )
    outputs: dict[str, Path] = {}
    for name, cwd, pkg in targets:
        out_path = bin_dir / name
        result = subprocess.run(
            ["go", "build", "-o", str(out_path), pkg],
            cwd=cwd,
            env=env,
            capture_output=True,
            text=True,
            timeout=180,
        )
        assert result.returncode == 0, (
            f"failed to build {name} from {cwd}: {result.stdout}\n{result.stderr}"
        )
        outputs[name] = out_path
    return BuiltBinaries(
        gateway=outputs["doops-gateway"],
        agent=outputs["doops-agent"],
        cli=outputs["doops"],
    )


@pytest.fixture(scope="module")
def e2e_stack(
    built_binaries: BuiltBinaries, tmp_path_factory: pytest.TempPathFactory
) -> LiveE2EStack:
    run_dir = tmp_path_factory.mktemp("doops-e2e-stack")
    db_path = run_dir / "gateway.db"
    workspace_root = run_dir / "workspaces"
    workspace_root.mkdir(parents=True, exist_ok=True)
    agent_skills_root = run_dir / "agent-skills"
    agent_skills_root.mkdir(parents=True, exist_ok=True)
    cli_state_dir = run_dir / "cli-state"
    cli_state_dir.mkdir(parents=True, exist_ok=True)
    config_path = cli_state_dir / "config.json"

    gateway_port = _free_tcp_port()
    agent_health_port = _free_tcp_port()
    synthetic_secret_key = "e2e-synthetic-master-key-" + secrets.token_hex(12)

    gw_env = _clean_loopback_env(
        drop_prefixes=("DOOPS_GATEWAY_", "DOOPS_AUDIT_", "DOOPS_REQUIRE_", "DOOPS_ALLOW_", "DOOPS_CONSOLE_")
    )
    gw_env["DOOPS_GATEWAY_SECRET_KEY"] = synthetic_secret_key
    gw_env["DOOPS_GATEWAY_STORE"] = "sqlite"

    # 1. Provision isolated SQLite DB using real doops-gateway subcommands.
    user_res = subprocess.run(
        [str(built_binaries.gateway), "user", "create", "-name", "e2e-admin", "-password-stdin", "-db", str(db_path)],
        env=gw_env,
        input="E2eSyntheticPassword!2026\n",
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert user_res.returncode == 0, f"user create failed: {user_res.stdout}\n{user_res.stderr}"

    for grant_args in (
        ["grant", "-user", "e2e-admin", "-admin", "-db", str(db_path)],
        ["grant", "-user", "e2e-admin", "-cluster", CLUSTER, "-instance", INSTANCE, "-db", str(db_path)],
    ):
        grant_res = subprocess.run(
            [str(built_binaries.gateway), *grant_args],
            env=gw_env,
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert grant_res.returncode == 0, f"{grant_args} failed: {grant_res.stdout}\n{grant_res.stderr}"

    token_res = subprocess.run(
        [str(built_binaries.gateway), "token", "create", "-user", "e2e-admin", "-name", "e2e-cli", "-db", str(db_path)],
        env=gw_env,
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert token_res.returncode == 0, f"token create failed: {token_res.stdout}\n{token_res.stderr}"
    token = ""
    for line in token_res.stdout.splitlines():
        if line.startswith("token="):
            token = line.split("=", 1)[1].strip()
            break
    assert token, f"could not parse token from gateway output: {token_res.stdout!r}"

    gateway_log = run_dir / "gateway.log"
    agent_log = run_dir / "agent.log"
    gw_proc: subprocess.Popen[bytes] | None = None
    agent_proc: subprocess.Popen[bytes] | None = None
    stack = LiveE2EStack(
        binaries=built_binaries,
        run_dir=run_dir,
        workspace_root=workspace_root,
        gateway_port=gateway_port,
        agent_health_port=agent_health_port,
        token=token,
        config_path=config_path,
        gateway_log=gateway_log,
        agent_log=agent_log,
    )

    gw_log_fp = gateway_log.open("wb")
    agent_log_fp = agent_log.open("wb")
    try:
        # 2. Start real doops-gateway serve.
        gw_proc = subprocess.Popen(
            [
                str(built_binaries.gateway),
                "serve",
                "-port",
                str(gateway_port),
                "-db",
                str(db_path),
                "-monitor-tick",
                "0",
                "-shutdown-drain-timeout",
                "2s",
            ],
            env=gw_env,
            stdout=gw_log_fp,
            stderr=subprocess.STDOUT,
        )

        deadline = time.monotonic() + 20.0
        while time.monotonic() < deadline:
            assert gw_proc.poll() is None, (
                f"doops-gateway exited early (code={gw_proc.returncode}){stack.diag()}"
            )
            try:
                status, _ = _http_get(f"{stack.gateway_url}/health", timeout=1.0)
                if status == 200:
                    break
            except OSError:
                pass
            time.sleep(0.05)
        else:
            pytest.fail(f"doops-gateway did not become healthy within 20s{stack.diag()}")

        # 3. Start real doops-agent connecting over the real WebSocket reverse tunnel.
        agent_env = _clean_loopback_env(
            drop_prefixes=("DOOPS_", "DO_AGENT_")
        )
        agent_env["DOOPS_WORKSPACE_ROOT"] = str(workspace_root)
        agent_env["DOOPS_AGENT_SKILLS_ROOT"] = str(agent_skills_root)

        agent_proc = subprocess.Popen(
            [
                str(built_binaries.agent),
                "-listen",
                "127.0.0.1",
                "-port",
                str(agent_health_port),
                "-gateway-url",
                stack.gateway_url,
                "-cluster",
                CLUSTER,
                "-instance",
                INSTANCE,
                "-reconnect-delay",
                "200ms",
            ],
            env=agent_env,
            stdout=agent_log_fp,
            stderr=subprocess.STDOUT,
        )

        # Wait until the real Agent appears online on Gateway /v1/targets AND /health/ready == 200.
        deadline = time.monotonic() + 20.0
        auth_headers = {"Authorization": f"Bearer {token}"}
        while time.monotonic() < deadline:
            assert gw_proc.poll() is None, (
                f"doops-gateway exited while waiting for agent{stack.diag()}"
            )
            assert agent_proc.poll() is None, (
                f"doops-agent exited early (code={agent_proc.returncode}){stack.diag()}"
            )
            try:
                status, body = _http_get(
                    f"{stack.gateway_url}/v1/targets", headers=auth_headers, timeout=1.0
                )
                if status == 200:
                    payload = json.loads(body.decode("utf-8"))
                    targets = payload.get("targets") or []
                    if any(
                        t.get("cluster") == CLUSTER and t.get("instance") == INSTANCE
                        for t in targets
                    ):
                        ready_status, _ = _http_get(
                            f"http://127.0.0.1:{agent_health_port}/health/ready", timeout=1.0
                        )
                        if ready_status == 200:
                            break
            except (OSError, json.JSONDecodeError):
                pass
            time.sleep(0.05)
        else:
            pytest.fail(
                f"doops-agent {CLUSTER}/{INSTANCE} did not register on /v1/targets within 20s{stack.diag()}"
            )

        # 4. Configure isolated CLI route & cached token via real CLI commands.
        add_res = stack.run_cli(
            [
                "add",
                "--name",
                TARGET_NAME,
                "--gateway",
                stack.gateway_url,
                "--cluster",
                CLUSTER,
                "--instance",
                INSTANCE,
                "--use",
                "Loopback E2E target",
            ]
        )
        assert add_res.returncode == 0, f"doops add failed: {add_res.stdout}\n{add_res.stderr}"

        login_res = stack.run_cli(
            ["login", "--target", TARGET_NAME, "--token-stdin"],
            stdin_text=token + "\n",
        )
        assert login_res.returncode == 0, (
            f"doops login failed: {login_res.stdout}\n{login_res.stderr}{stack.diag()}"
        )

        yield stack
    finally:
        _terminate_process(agent_proc)
        _terminate_process(gw_proc)
        gw_log_fp.close()
        agent_log_fp.close()
        for session_id in stack.tracked_sessions:
            shutil.rmtree(Path("/tmp/repos") / f"{session_id}.git", ignore_errors=True)


def test_push_gateway_storage_syncs_files_and_ready_sentinel(
    e2e_stack: LiveE2EStack, tmp_path: Path
) -> None:
    """Scenario 1: `doops push --storage gateway` (--dry-run, initial push, incremental sync)."""
    session_id = e2e_stack.track_session(_unique_session("e2e-push"))
    src_dir = tmp_path / "chart-src"
    (src_dir / "templates").mkdir(parents=True)
    chart_v1 = "apiVersion: v2\nname: e2e-demo\nversion: 0.1.0\n"
    values_v1 = "replicaCount: 2\nimage:\n  tag: v1\n"
    template_v1 = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: e2e-cm\n"
    stale_v1 = "remove me on next push\n"

    (src_dir / "Chart.yaml").write_text(chart_v1, encoding="utf-8")
    (src_dir / "values.yaml").write_text(values_v1, encoding="utf-8")
    (src_dir / "templates" / "configmap.yaml").write_text(template_v1, encoding="utf-8")
    (src_dir / "stale.txt").write_text(stale_v1, encoding="utf-8")

    remote_ws = e2e_stack.workspace_root / session_id

    # 1a. Dry-run previews files without mutating remote workspace.
    dry = e2e_stack.run_cli(
        [
            "-session",
            session_id,
            "push",
            "--storage",
            "gateway",
            "--target",
            TARGET_NAME,
            "--src",
            str(src_dir),
            "--dry-run",
        ]
    )
    assert dry.returncode == 0, f"push --dry-run failed: {dry.stdout}\n{dry.stderr}"
    assert "[DRY-RUN]" in dry.stdout and "Chart.yaml" in dry.stdout
    assert not remote_ws.exists(), f"--dry-run unexpectedly created {remote_ws}"

    # 1b. Real initial push via Gateway Git HTTP reverse tunnel + real doops_shell ready check.
    first = e2e_stack.run_cli(
        [
            "-session",
            session_id,
            "push",
            "--storage",
            "gateway",
            "--target",
            TARGET_NAME,
            "--src",
            str(src_dir),
        ]
    )
    assert first.returncode == 0, (
        f"initial push --storage gateway failed (rc={first.returncode}):\n"
        f"stdout={first.stdout}\nstderr={first.stderr}{e2e_stack.diag()}"
    )
    assert "同步完成" in first.stdout, f"missing 同步完成 in push stdout: {first.stdout!r}"

    assert (remote_ws / "Chart.yaml").read_text(encoding="utf-8") == chart_v1
    assert (remote_ws / "values.yaml").read_text(encoding="utf-8") == values_v1
    assert (remote_ws / "templates" / "configmap.yaml").read_text(encoding="utf-8") == template_v1
    assert (remote_ws / "stale.txt").read_text(encoding="utf-8") == stale_v1

    ready_v1 = (remote_ws / ".doops-ready").read_text(encoding="utf-8").strip()
    assert SHA40_RE.match(ready_v1), f"invalid .doops-ready SHA: {ready_v1!r}"
    assert ready_v1[:12] in first.stdout, (
        f"push stdout should reference short commit {ready_v1[:12]}: {first.stdout!r}"
    )

    # 1c. Incremental update push: modify Chart.yaml, add README.md, delete stale.txt.
    chart_v2 = "apiVersion: v2\nname: e2e-demo\nversion: 0.2.0\n"
    readme_v2 = "# Updated E2E Chart\n"
    (src_dir / "Chart.yaml").write_text(chart_v2, encoding="utf-8")
    (src_dir / "README.md").write_text(readme_v2, encoding="utf-8")
    (src_dir / "stale.txt").unlink()

    second = e2e_stack.run_cli(
        [
            "-session",
            session_id,
            "push",
            "--storage",
            "gateway",
            "--target",
            TARGET_NAME,
            "--src",
            str(src_dir),
        ]
    )
    assert second.returncode == 0, (
        f"incremental push failed (rc={second.returncode}):\n"
        f"stdout={second.stdout}\nstderr={second.stderr}{e2e_stack.diag()}"
    )
    assert "同步完成" in second.stdout
    assert (remote_ws / "Chart.yaml").read_text(encoding="utf-8") == chart_v2
    assert (remote_ws / "README.md").read_text(encoding="utf-8") == readme_v2
    assert not (remote_ws / "stale.txt").exists(), "stale file was not cleaned on update push"

    ready_v2 = (remote_ws / ".doops-ready").read_text(encoding="utf-8").strip()
    assert SHA40_RE.match(ready_v2), f"invalid updated .doops-ready SHA: {ready_v2!r}"
    assert ready_v2 != ready_v1, "incremental push must advance .doops-ready commit SHA"


@pytest.mark.parametrize("flag", ["--raw", "--quiet"])
def test_exec_raw_and_quiet_emit_pure_json_stdout(
    e2e_stack: LiveE2EStack, flag: str
) -> None:
    """Scenario 2: `doops exec --raw` and `--quiet` emit pure machine-parseable JSON stdout."""
    session_id = _unique_session("e2e-exec")
    mode = flag.lstrip("-")
    cmd = f"""printf '{{"ok":true,"name":"%s","mode":"%s"}}\\n' "loopback-e2e" "{mode}" """.strip()

    res = e2e_stack.run_cli(
        ["-session", session_id, "exec", "--target", TARGET_NAME, flag, "--cmd", cmd]
    )
    assert res.returncode == 0, (
        f"exec {flag} failed (rc={res.returncode}): stdout={res.stdout!r} stderr={res.stderr!r}"
        f"{e2e_stack.diag()}"
    )
    assert "[TARGETING]" not in res.stdout
    assert "Operation complete." not in res.stdout
    assert "\x1b" not in res.stdout
    assert "命令后台执行中" not in res.stdout

    parsed = json.loads(res.stdout)
    assert parsed == {"ok": True, "name": "loopback-e2e", "mode": mode}

    # Verify contrast: non-raw exec DOES print the human [TARGETING] banner, and
    # raw-mode non-zero exits route errors to stderr while keeping stdout empty.
    if flag == "--raw":
        human = e2e_stack.run_cli(
            ["-session", session_id, "exec", "--target", TARGET_NAME, "--cmd", "printf 'hello\\n'"]
        )
        assert human.returncode == 0
        assert "[TARGETING]" in human.stdout
        assert "hello" in human.stdout

        failing = e2e_stack.run_cli(
            ["-session", session_id, "exec", "--target", TARGET_NAME, "--raw", "--cmd", "exit 7"]
        )
        assert failing.returncode != 0
        assert failing.stdout == "", f"--raw failure leaked text into stdout: {failing.stdout!r}"
        assert "Error:" in failing.stderr


def test_pull_gateway_storage_roundtrip_syncs_remote_edits(
    e2e_stack: LiveE2EStack, tmp_path: Path
) -> None:
    """Scenario 3: push -> remote workspace edit via real exec -> `doops pull --storage gateway`."""
    session_id = e2e_stack.track_session(_unique_session("e2e-pull"))
    src_dir = tmp_path / "pull-src"
    src_dir.mkdir()
    (src_dir / "Chart.yaml").write_text("apiVersion: v2\nname: pull-demo\nversion: 1.0.0\n", encoding="utf-8")
    (src_dir / "keep.txt").write_text("untouched-original\n", encoding="utf-8")

    push_res = e2e_stack.run_cli(
        [
            "-session",
            session_id,
            "push",
            "--storage",
            "gateway",
            "--target",
            TARGET_NAME,
            "--src",
            str(src_dir),
        ]
    )
    assert push_res.returncode == 0, f"push failed: {push_res.stdout}\n{push_res.stderr}{e2e_stack.diag()}"

    # Mutate the remote workspace through real `doops exec --raw`.
    remote_edit_cmd = (
        f'set -e; ws="$DOOPS_WORKSPACE_ROOT/{session_id}"; '
        'printf "apiVersion: v2\\nname: pull-demo\\nversion: 1.1.0-remote\\n" > "$ws/Chart.yaml"; '
        'mkdir -p "$ws/notes"; '
        'printf "edited-on-agent\\n" > "$ws/notes/remote-edit.txt"; '
        'printf \'{"edited":true}\\n\''
    )
    edit_res = e2e_stack.run_cli(
        ["-session", session_id, "exec", "--target", TARGET_NAME, "--raw", "--cmd", remote_edit_cmd]
    )
    assert edit_res.returncode == 0, f"remote edit exec failed: {edit_res.stdout}\n{edit_res.stderr}"
    assert json.loads(edit_res.stdout) == {"edited": True}

    pull_dest = tmp_path / "pull-dest"
    pull_res = e2e_stack.run_cli(
        [
            "-session",
            session_id,
            "pull",
            "--storage",
            "gateway",
            "--target",
            TARGET_NAME,
            "--dest",
            str(pull_dest),
        ]
    )
    assert pull_res.returncode == 0, (
        f"pull --storage gateway failed (rc={pull_res.returncode}):\n"
        f"stdout={pull_res.stdout}\nstderr={pull_res.stderr}{e2e_stack.diag()}"
    )
    assert (pull_dest / "Chart.yaml").read_text(encoding="utf-8") == (
        "apiVersion: v2\nname: pull-demo\nversion: 1.1.0-remote\n"
    )
    assert (pull_dest / "notes" / "remote-edit.txt").read_text(encoding="utf-8") == "edited-on-agent\n"
    assert (pull_dest / "keep.txt").read_text(encoding="utf-8") == "untouched-original\n"


def test_clean_session_reuse_and_fail_closed_contract(
    e2e_stack: LiveE2EStack, tmp_path: Path
) -> None:
    """Scenario 4: `doops clean` reuses explicit `-session` and fails closed when both omitted."""
    src_dir = tmp_path / "clean-src"
    src_dir.mkdir()
    (src_dir / "Chart.yaml").write_text("apiVersion: v2\nname: clean-demo\nversion: 0.1.0\n", encoding="utf-8")

    # 4a. Push a workspace, then clean with explicit `-session <S>` and omitted `--workspace`.
    clean_sid = e2e_stack.track_session(_unique_session("e2e-clean"))
    push_clean = e2e_stack.run_cli(
        [
            "-session",
            clean_sid,
            "push",
            "--storage",
            "gateway",
            "--target",
            TARGET_NAME,
            "--src",
            str(src_dir),
        ]
    )
    assert push_clean.returncode == 0, f"push failed: {push_clean.stdout}\n{push_clean.stderr}"
    remote_clean_ws = e2e_stack.workspace_root / clean_sid
    remote_clean_repo = Path("/tmp/repos") / f"{clean_sid}.git"
    assert remote_clean_ws.is_dir(), f"expected remote workspace {remote_clean_ws}"
    assert remote_clean_repo.is_dir(), f"expected remote bare repo {remote_clean_repo}"

    clean_res = e2e_stack.run_cli(["-session", clean_sid, "clean", "--target", TARGET_NAME])
    assert clean_res.returncode == 0, (
        f"clean with explicit -session failed: {clean_res.stdout}\n{clean_res.stderr}{e2e_stack.diag()}"
    )
    assert not remote_clean_ws.exists(), f"remote workspace still exists after clean: {remote_clean_ws}"
    assert not remote_clean_repo.exists(), f"bare repo still exists after clean: {remote_clean_repo}"

    # 4b. Fail closed when neither `-session` nor `--workspace` is provided.
    keep_sid = e2e_stack.track_session(_unique_session("e2e-keep"))
    push_keep = e2e_stack.run_cli(
        [
            "-session",
            keep_sid,
            "push",
            "--storage",
            "gateway",
            "--target",
            TARGET_NAME,
            "--src",
            str(src_dir),
        ]
    )
    assert push_keep.returncode == 0, f"sentinel push failed: {push_keep.stdout}\n{push_keep.stderr}"
    sentinel_ws = e2e_stack.workspace_root / keep_sid
    assert sentinel_ws.is_dir()

    fail_closed = e2e_stack.run_cli(["clean", "--target", TARGET_NAME])
    assert fail_closed.returncode != 0, (
        f"clean without -session/--workspace must fail closed: stdout={fail_closed.stdout!r}"
    )
    combined = fail_closed.stdout + fail_closed.stderr
    assert "--workspace" in combined and "-session" in combined, f"unexpected error output: {combined!r}"
    assert sentinel_ws.is_dir(), "fail-closed clean must never delete unrelated workspaces"


def _configure_proxy_cli(
    e2e_stack: LiveE2EStack, proxy_url: str, config_path: Path, target_name: str
) -> None:
    add_res = e2e_stack.run_cli(
        [
            "add",
            "--name",
            target_name,
            "--gateway",
            proxy_url,
            "--cluster",
            CLUSTER,
            "--instance",
            INSTANCE,
            "--use",
            "Reverse-proxy E2E target",
        ],
        config_path=config_path,
    )
    assert add_res.returncode == 0, f"proxy add failed: {add_res.stdout}\n{add_res.stderr}"

    login_res = e2e_stack.run_cli(
        ["login", "--target", target_name, "--token-stdin"],
        config_path=config_path,
        stdin_text=e2e_stack.token + "\n",
    )
    assert login_res.returncode == 0, (
        f"proxy login failed: {login_res.stdout}\n{login_res.stderr}{e2e_stack.diag()}"
    )


def test_rpc_reverse_proxy_retries_502_503_and_zero_retries_401(
    e2e_stack: LiveE2EStack, tmp_path: Path
) -> None:
    """Scenario 5: real HTTP/WS reverse proxy verifies 502->503 retry and 401 zero-retry."""
    # 5a. Transient 502 then 503 then passthrough -> CLI retries transparently and succeeds.
    with _FaultInjectingReverseProxy(e2e_stack.gateway_port, rpc_faults=[502, 503]) as proxy:
        cfg_retry = tmp_path / "proxy-retry" / "config.json"
        _configure_proxy_cli(e2e_stack, proxy.url, cfg_retry, "proxy-retry-target")

        res = e2e_stack.run_cli(
            [
                "-session",
                _unique_session("e2e-retry"),
                "exec",
                "--target",
                "proxy-retry-target",
                "--raw",
                "--cmd",
                """printf '{"recovered":true,"attempts":3}\\n'""",
            ],
            config_path=cfg_retry,
            timeout=30.0,
        )
        assert res.returncode == 0, (
            f"CLI failed to recover after 502/503 handshake faults (rc={res.returncode}):\n"
            f"stdout={res.stdout!r}\nstderr={res.stderr!r}\n"
            f"rpc_attempts={proxy.rpc_attempts}{e2e_stack.diag()}"
        )
        assert json.loads(res.stdout) == {"recovered": True, "attempts": 3}
        assert proxy.rpc_attempts == [502, 503, "passthrough"], (
            f"unexpected /v1/rpc handshake sequence: {proxy.rpc_attempts}"
        )

    # 5b. Permanent 401 Unauthorized on /v1/rpc -> CLI fails on attempt 1 with ZERO retries.
    with _FaultInjectingReverseProxy(e2e_stack.gateway_port, default_rpc_fault=401) as proxy_401:
        cfg_401 = tmp_path / "proxy-401" / "config.json"
        _configure_proxy_cli(e2e_stack, proxy_401.url, cfg_401, "proxy-401-target")

        res_401 = e2e_stack.run_cli(
            [
                "-session",
                _unique_session("e2e-401"),
                "exec",
                "--target",
                "proxy-401-target",
                "--raw",
                "--cmd",
                "echo must-not-execute",
            ],
            config_path=cfg_401,
            timeout=15.0,
        )
        assert res_401.returncode != 0, (
            f"CLI unexpectedly succeeded despite 401 on /v1/rpc: stdout={res_401.stdout!r}"
        )
        assert res_401.stdout == "", f"401 failure leaked into --raw stdout: {res_401.stdout!r}"
        assert proxy_401.rpc_attempts == [401], (
            f"401 Unauthorized must trigger strictly 1 handshake attempt (zero retries), "
            f"got {proxy_401.rpc_attempts}"
        )
        assert "401" in res_401.stderr or "unauthorized" in res_401.stderr.lower(), (
            f"expected 401 diagnostic on stderr, got: {res_401.stderr!r}"
        )
