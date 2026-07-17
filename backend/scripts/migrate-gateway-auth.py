#!/usr/bin/env python3
import argparse
import json
import os
import tempfile
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit


def gateway_key(raw: str) -> str:
    raw = raw.strip()
    if "://" not in raw:
        raw = "https://" + raw
    parsed = urlsplit(raw)
    scheme = {"ws": "http", "wss": "https"}.get(parsed.scheme.lower(), parsed.scheme.lower())
    host = (parsed.hostname or "").lower()
    if ":" in host:
        host = f"[{host}]"
    port = parsed.port
    if (scheme, port) in {("http", 80), ("https", 443)}:
        port = None
    netloc = host if port is None else f"{host}:{port}"
    path = parsed.path.rstrip("/")
    return urlunsplit((scheme, netloc, path, "", "")).rstrip("/")


def write_json_atomic(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as handle:
            json.dump(value, handle, indent=4)
            handle.write("\n")
        os.chmod(temp_name, 0o600)
        os.replace(temp_name, path)
    finally:
        if os.path.exists(temp_name):
            os.unlink(temp_name)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Migrate one Gateway from per-target tokens to one Gateway login state."
    )
    parser.add_argument("--target", required=True, help="Target whose current token should become authoritative")
    parser.add_argument(
        "--config",
        default=str(Path.home() / ".agent/skills/doops/config.json"),
        help="Canonical DoOps config path",
    )
    args = parser.parse_args()

    config_path = Path(args.config).expanduser().resolve()
    auth_path = config_path.with_name("auth.json")
    config = json.loads(config_path.read_text())
    servers = config.get("servers", [])
    selected = next(
        (
            server
            for server in servers
            if server.get("name") == args.target or args.target in server.get("aliases", [])
        ),
        None,
    )
    if selected is None:
        raise SystemExit(f"target not found: {args.target}")

    gateway = gateway_key(str(selected.get("gateway", "")))
    token = str(selected.get("token", "")).strip()
    if not gateway or not token:
        raise SystemExit("selected target must contain both gateway and token")

    migrated = 0
    for server in servers:
        if gateway_key(str(server.get("gateway", ""))) == gateway and "token" in server:
            del server["token"]
            migrated += 1

    auth = {"tokens": {}}
    if auth_path.exists():
        auth = json.loads(auth_path.read_text())
        if not isinstance(auth.get("tokens"), dict):
            raise SystemExit("auth.json must contain a tokens object")
    auth["tokens"] = {
        key: value
        for key, value in auth["tokens"].items()
        if key.startswith("http://") or key.startswith("https://")
    }
    auth["tokens"][gateway] = token

    # Write auth first. If config replacement fails, rerunning is safe and the
    # old CLI still has its per-target token.
    write_json_atomic(auth_path, auth)
    write_json_atomic(config_path, config)
    print(f"migrated gateway={gateway} targets={migrated}")


if __name__ == "__main__":
    main()
