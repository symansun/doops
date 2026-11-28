#!/usr/bin/env python3
"""Standalone zero-dependency stdio MCP Server packaged inside the DoDNS plugin.

Runs completely outside the DoOps Agent binary: synced with the plugin package
under <skills_root>/dodns/mcp/server.py and invoked via platform_mcp_servers.
Enforces path confinement, zero plaintext secret inputs, timestamped backups,
SOA bumping, named-checkzone / rndc reload validation, and automatic rollback.
"""
from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent.parent / "scripts"
sys.path.insert(0, str(SCRIPT_DIR))
import dodns  # noqa: E402

SECRET_KEYS = {
    "password", "passwd", "secret", "token", "api_key", "apikey",
    "access_key", "access_key_secret", "private_key", "authorization",
}
ALLOWED_BIND_ROOTS = ("/etc/bind", "/var/named", "/etc/named", "/var/cache/bind")

TOOLS = [
    {
        "name": "query",
        "description": "Send a read-only raw UDP DNS wire query (zero dig dependency).",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "required": ["server", "name"],
            "properties": {
                "server": {"type": "string"},
                "port": {"type": "integer", "minimum": 1, "maximum": 65535},
                "name": {"type": "string"},
                "type": {"type": "string", "enum": sorted(dodns.QTYPE)},
                "norecurse": {"type": "boolean"},
            },
        },
    },
    {
        "name": "check_recursion",
        "description": "Probe whether a DNS server answers open recursive queries (RD=1 -> RA=1).",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "required": ["server"],
            "properties": {
                "server": {"type": "string"},
                "port": {"type": "integer", "minimum": 1, "maximum": 65535},
                "probe_name": {"type": "string"},
            },
        },
    },
    {
        "name": "zone_render",
        "description": "Render a minimal authoritative BIND zone template without writing files.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "required": ["zone", "ns_host", "ns_ip"],
            "properties": {
                "zone": {"type": "string"},
                "ns_host": {"type": "string"},
                "ns_ip": {"type": "string"},
            },
        },
    },
    {
        "name": "zone_check",
        "description": "Read-only syntax check of a confined BIND zone file using named-checkzone when installed.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "required": ["zone", "zone_file"],
            "properties": {
                "zone": {"type": "string"},
                "zone_file": {"type": "string"},
            },
        },
    },
    {
        "name": "record_add",
        "description": "Add a DNS record to a BIND zone file with backup, SOA bump, named-checkzone, optional rndc reload, and automatic rollback on failure.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "required": ["zone_file", "name", "type", "value"],
            "properties": {
                "zone": {"type": "string"},
                "zone_file": {"type": "string"},
                "name": {"type": "string"},
                "type": {"type": "string", "enum": sorted(dodns.SUPPORTED_RECORD_TYPES)},
                "value": {"type": "string"},
                "ttl": {"type": "integer", "minimum": 1, "maximum": 604800},
                "dry_run": {"type": "boolean"},
                "reload": {"type": "boolean"},
            },
        },
    },
    {
        "name": "record_replace",
        "description": "Replace a DNS record in a BIND zone file with backup, SOA bump, validation, and automatic rollback on failure.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "required": ["zone_file", "name", "type", "value"],
            "properties": {
                "zone": {"type": "string"},
                "zone_file": {"type": "string"},
                "name": {"type": "string"},
                "type": {"type": "string", "enum": sorted(dodns.SUPPORTED_RECORD_TYPES)},
                "value": {"type": "string"},
                "ttl": {"type": "integer", "minimum": 1, "maximum": 604800},
                "dry_run": {"type": "boolean"},
                "reload": {"type": "boolean"},
            },
        },
    },
    {
        "name": "record_delete",
        "description": "Delete a DNS record from a BIND zone file with backup, SOA bump, validation, and automatic rollback on failure.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "required": ["zone_file", "name", "type"],
            "properties": {
                "zone": {"type": "string"},
                "zone_file": {"type": "string"},
                "name": {"type": "string"},
                "type": {"type": "string", "enum": sorted(dodns.SUPPORTED_RECORD_TYPES)},
                "dry_run": {"type": "boolean"},
                "reload": {"type": "boolean"},
            },
        },
    },
    {
        "name": "alidns_ensure",
        "description": "Idempotently ensure an AliDNS record using DoOps runtime-opaque credential file (never accepts secrets in arguments).",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "required": ["domain", "rr", "value"],
            "properties": {
                "domain": {"type": "string"},
                "rr": {"type": "string"},
                "type": {"type": "string", "enum": ["A", "AAAA", "CNAME", "TXT", "MX"]},
                "value": {"type": "string"},
                "ttl": {"type": "integer", "minimum": 1, "maximum": 86400},
                "dry_run": {"type": "boolean"},
                "wait_public_seconds": {"type": "integer", "minimum": 0, "maximum": 300},
            },
        },
    },
]


def reject_secrets(obj, path=""):
    if isinstance(obj, dict):
        for k, v in obj.items():
            kl = str(k).lower()
            if kl in SECRET_KEYS or "password" in kl or "secret" in kl or "private_key" in kl:
                raise ValueError(f"forbidden sensitive argument {path + k!r}")
            reject_secrets(v, f"{path}{k}.")
    elif isinstance(obj, list):
        for i, item in enumerate(obj):
            reject_secrets(item, f"{path}[{i}].")


def resolve_zone_file(raw: str) -> Path:
    val = (raw or "").strip()
    if not val or "\x00" in val or ".." in Path(val).parts:
        raise ValueError("zone_file contains disallowed path traversal")
    p = Path(val)
    if not p.is_absolute():
        cwd = Path.cwd().resolve()
        resolved = (cwd / p).resolve()
        if cwd not in resolved.parents:
            raise ValueError("relative zone_file escapes working directory")
        return resolved
    resolved = p.resolve()
    roots = list(ALLOWED_BIND_ROOTS)
    extra = os.environ.get("DODNS_ALLOWED_ZONE_ROOT", "").strip()
    if extra and Path(extra).is_absolute() and ".." not in Path(extra).parts:
        roots.append(extra)
    for root in roots:
        root_p = Path(root).resolve() if Path(root).exists() else Path(root)
        if root_p in resolved.parents:
            return resolved
    raise ValueError("zone_file must be relative to workspace or under /etc/bind, /var/named, /etc/named, /var/cache/bind")


def resolve_bin(env_name: str, default_name: str) -> str | None:
    custom = os.environ.get(env_name, "").strip()
    if custom:
        return custom
    return shutil.which(default_name)


def run_zone_mutation(op: str, args: dict) -> dict:
    zone_path = resolve_zone_file(args["zone_file"])
    text = zone_path.read_text(encoding="utf-8")
    name = str(args["name"]).strip()
    rtype = str(args["type"]).strip().upper()
    dry_run = bool(args.get("dry_run", False))

    if op == "record_add":
        updated, serial = dodns.append_record(text, dodns.Record(name, rtype, str(args["value"]).strip(), args.get("ttl")))
        changed = 1
    elif op == "record_replace":
        updated, serial, changed = dodns.replace_record(text, dodns.Record(name, rtype, str(args["value"]).strip(), args.get("ttl")))
    elif op == "record_delete":
        updated, serial, changed = dodns.delete_record(text, name, rtype)
    else:
        raise ValueError(f"unsupported mutation {op}")

    if dry_run:
        return {"operation": op, "dry_run": True, "zone_file": str(zone_path), "serial": serial, "changed": changed}

    backup = dodns._write_with_backup(str(zone_path), updated)
    result = {"operation": op, "dry_run": False, "zone_file": str(zone_path), "backup": backup, "serial": serial, "changed": changed}

    def rollback(stage: str, msg: str):
        shutil.copy2(backup, zone_path)
        raise RuntimeError(f"{stage} failed ({msg}); zone file automatically restored from backup {Path(backup).name}")

    zone = str(args.get("zone") or "").strip()
    if zone:
        check_bin = resolve_bin("DODNS_CHECKZONE_BIN", "named-checkzone")
        if check_bin:
            proc = subprocess.run([check_bin, zone, str(zone_path)], capture_output=True, text=True, timeout=30)
            if proc.returncode != 0:
                rollback("named-checkzone", (proc.stderr or proc.stdout).strip())
            result["checkzone"] = "ok"
        else:
            result["checkzone"] = "binary_not_installed"

    if args.get("reload"):
        rndc_bin = resolve_bin("DODNS_RNDC_BIN", "rndc")
        if not rndc_bin:
            rollback("rndc_reload", "rndc binary not installed")
        cmd = [rndc_bin, "reload"] + ([zone] if zone else [])
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        if proc.returncode != 0:
            rollback("rndc_reload", (proc.stderr or proc.stdout).strip())
        result["reload"] = "ok"

    return result


def dispatch_tool(name: str, args: dict) -> str:
    if not isinstance(args, dict):
        raise ValueError("arguments must be an object")
    reject_secrets(args)
    if name == "query":
        res = dodns.raw_udp_query(
            str(args["server"]).strip(),
            str(args["name"]).strip(),
            str(args.get("type") or "A").upper(),
            int(args.get("port") or 53),
            rd=not bool(args.get("norecurse", False)),
        )
        return json.dumps(res, ensure_ascii=False)
    if name == "check_recursion":
        res = dodns.raw_udp_query(
            str(args["server"]).strip(),
            str(args.get("probe_name") or "example.com").strip(),
            "A",
            int(args.get("port") or 53),
            rd=True,
        )
        out = {"server": res["server"], "open_recursion": bool(res["ra"] and res["rcode"] == 0), "ra": res["ra"], "rcode": res["rcode"]}
        return json.dumps(out, ensure_ascii=False)
    if name == "zone_render":
        return dodns.render_minimal_zone(str(args["zone"]), str(args["ns_host"]), str(args["ns_ip"]))
    if name == "zone_check":
        zone_path = resolve_zone_file(str(args["zone_file"]))
        zone = str(args["zone"]).strip()
        check_bin = resolve_bin("DODNS_CHECKZONE_BIN", "named-checkzone")
        if check_bin:
            proc = subprocess.run([check_bin, zone, str(zone_path)], capture_output=True, text=True, timeout=30)
            if proc.returncode != 0:
                raise RuntimeError(f"named-checkzone failed: {(proc.stderr or proc.stdout).strip()}")
            return json.dumps({"zone": zone, "zone_file": str(zone_path), "checkzone": "ok", "output": proc.stdout.strip()}, ensure_ascii=False)
        raw = zone_path.read_text(encoding="utf-8")
        return json.dumps({"zone": zone, "zone_file": str(zone_path), "checkzone": "binary_not_installed", "has_soa": "SOA" in raw.upper()}, ensure_ascii=False)
    if name in ("record_add", "record_replace", "record_delete"):
        return json.dumps(run_zone_mutation(name, args), ensure_ascii=False)
    if name == "alidns_ensure":
        node_bin = os.environ.get("DODNS_NODE", "node")
        script = str(SCRIPT_DIR / "ensure-alidns-record.mjs")
        cmd = [
            node_bin, script,
            "--domain", str(args["domain"]).strip(),
            "--rr", str(args["rr"]).strip(),
            "--value", str(args["value"]).strip(),
        ]
        if args.get("type"):
            cmd += ["--type", str(args["type"]).upper()]
        if args.get("ttl"):
            cmd += ["--ttl", str(int(args["ttl"]))]
        if args.get("dry_run"):
            cmd.append("--dry-run")
        if "wait_public_seconds" in args:
            cmd += ["--wait-public-seconds", str(int(args["wait_public_seconds"]))]
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
        if proc.returncode != 0:
            raise RuntimeError((proc.stderr or proc.stdout or "alidns_ensure failed").strip())
        return proc.stdout.strip()
    raise KeyError(f"unknown tool: {name}")


def main() -> int:
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except json.JSONDecodeError:
            sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "parse error"}}) + "\n")
            sys.stdout.flush()
            continue
        req_id = req.get("id")
        method = req.get("method")
        if method == "notifications/initialized":
            continue
        if method == "initialize":
            resp = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "protocolVersion": "2025-03-26",
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": "dodns-plugin-mcp", "version": "1.0.0"},
                },
            }
        elif method == "tools/list":
            resp = {"jsonrpc": "2.0", "id": req_id, "result": {"tools": TOOLS}}
        elif method == "tools/call":
            params = req.get("params") or {}
            tname = params.get("name", "")
            targs = params.get("arguments") or {}
            try:
                text = dispatch_tool(tname, targs)
                resp = {"jsonrpc": "2.0", "id": req_id, "result": {"content": [{"type": "text", "text": text}], "isError": False}}
            except KeyError as exc:
                resp = {"jsonrpc": "2.0", "id": req_id, "error": {"code": -32601, "message": str(exc)}}
            except Exception as exc:
                resp = {"jsonrpc": "2.0", "id": req_id, "result": {"content": [{"type": "text", "text": str(exc)}], "isError": True}}
        else:
            resp = {"jsonrpc": "2.0", "id": req_id, "error": {"code": -32601, "message": "method not found"}}
        sys.stdout.write(json.dumps(resp, ensure_ascii=False) + "\n")
        sys.stdout.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main())
