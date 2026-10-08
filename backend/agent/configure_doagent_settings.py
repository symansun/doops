#!/usr/bin/env python3
"""Materialize doagent runtime settings without mutating the mounted Secret."""

import json
import os
import sys
from pathlib import Path
from typing import Any


class SettingsConfigurationError(ValueError):
    """The declared model-routing policy and mounted settings disagree."""


TIER_NAMES = ("high", "default", "low")
DOOPS_MCP_SERVER = {
    "name": "doops",
    "command": "/app/doops-agent",
    "args": ["mcp-stdio", "--domain", "doops"],
    "env": {},
    "cwd": None,
    "status": "Unknown",
    "capabilities": ["tools"],
    "auto_start": True,
    "tool_timeout_secs": 900,
    "required": True,
    "trusted_result_attestation": True,
}
DOSQL_MCP_SERVER = {
    "name": "dosql",
    "command": "/app/doops-agent",
    "args": ["mcp-stdio", "--domain", "dosql"],
    "env": {},
    "cwd": None,
    "status": "Unknown",
    "capabilities": ["tools"],
    "auto_start": True,
    "tool_timeout_secs": 900,
    "required": True,
    "trusted_result_attestation": True,
}
RUNTIME_MCP_SERVERS = (DOOPS_MCP_SERVER, DOSQL_MCP_SERVER)
DOOPS_PERMISSIONS = {
    "default_mode": "restricted",
    "presets": {
        "always_allow": ["mcp_doops_k8s_read", "mcp_dosql_read"],
        "always_deny": [],
    },
    "timeout_seconds": 30,
}


def read_settings(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as error:
        raise SettingsConfigurationError("mounted doagent settings are required") from error
    except json.JSONDecodeError as error:
        raise SettingsConfigurationError("mounted doagent settings are not valid JSON") from error
    if not isinstance(payload, dict):
        raise SettingsConfigurationError("mounted doagent settings must be an object")
    return payload


def model_tiers(settings: dict[str, Any]) -> dict[str, str] | None:
    tiers = settings.get("model_tiers")
    if tiers is None:
        return None
    if not isinstance(tiers, dict):
        raise SettingsConfigurationError("model_tiers must be an object")
    values = {name: tiers.get(name) for name in TIER_NAMES}
    if any(not isinstance(value, str) or not value.strip() for value in values.values()):
        raise SettingsConfigurationError("model_tiers requires high, default, and low models")
    return {name: value.strip() for name, value in values.items()}


def resolve_policy(settings: dict[str, Any], declared_policy: str | None) -> str:
    if declared_policy:
        if declared_policy not in {"single-model", "tiered"}:
            raise SettingsConfigurationError("model routing policy must be single-model or tiered")
        return declared_policy

    tiers = model_tiers(settings)
    if tiers is None:
        return "single-model"
    if len(set(tiers.values())) == 1:
        return "single-model"
    if len(set(tiers.values())) == len(TIER_NAMES):
        return "tiered"
    raise SettingsConfigurationError(
        "mounted model_tiers are neither a single-model nor a distinct tiered policy"
    )


def configure_settings(
    source: Path,
    destination: Path,
    declared_policy: str | None,
) -> str:
    settings = read_settings(source)
    model = settings.get("model")
    if not isinstance(model, str) or not model.strip():
        raise SettingsConfigurationError("mounted doagent settings require model")
    model = model.strip()

    policy = resolve_policy(settings, declared_policy)
    tiers = model_tiers(settings)
    if policy == "single-model":
        if tiers is not None and (len(set(tiers.values())) != 1 or tiers["default"] != model):
            raise SettingsConfigurationError(
                "single-model policy requires all declared tiers to equal model"
            )
        settings.pop("model_tiers", None)
    elif tiers is None or len(set(tiers.values())) != len(TIER_NAMES):
        raise SettingsConfigurationError(
            "tiered policy requires three distinct model identifiers"
        )

    servers = settings.get("mcp_servers", [])
    if not isinstance(servers, list):
        raise SettingsConfigurationError("mcp_servers must be an array")
    for runtime_server in RUNTIME_MCP_SERVERS:
        configured = [
            server
            for server in servers
            if isinstance(server, dict) and server.get("name") == runtime_server["name"]
        ]
        if len(configured) > 1:
            raise SettingsConfigurationError(f"mcp_servers contains duplicate {runtime_server['name']} entries")
        if configured and configured[0] != runtime_server:
            raise SettingsConfigurationError(
                f"mcp_servers.{runtime_server['name']} is runtime-owned and must match the canonical configuration"
            )
    runtime_names = {item["name"] for item in RUNTIME_MCP_SERVERS}
    settings["mcp_servers"] = [
        server
        for server in servers
        if not (isinstance(server, dict) and server.get("name") in runtime_names)
    ]
    settings["mcp_servers"].extend(RUNTIME_MCP_SERVERS)
    settings["permissions"] = DOOPS_PERMISSIONS

    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_suffix(destination.suffix + ".tmp")
    temporary.write_text(
        json.dumps(settings, ensure_ascii=True, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    os.replace(temporary, destination)
    return policy


def main(argv: list[str]) -> int:
    if len(argv) not in {3, 4}:
        print(
            "usage: configure_doagent_settings.py <source> <destination> [policy]",
            file=sys.stderr,
        )
        return 2
    policy = argv[3].strip() if len(argv) == 4 else None
    try:
        effective_policy = configure_settings(Path(argv[1]), Path(argv[2]), policy or None)
    except SettingsConfigurationError as error:
        print(f"doagent settings configuration failed: {error}", file=sys.stderr)
        return 1
    print(f"doagent model routing policy: {effective_policy}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
