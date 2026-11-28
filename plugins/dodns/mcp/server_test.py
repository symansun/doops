#!/usr/bin/env python3
"""Tests for the standalone DoDNS plugin stdio MCP server."""
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SERVER_PY = Path(__file__).resolve().parent / "server.py"


def call_mcp(requests: list[dict], cwd: str, env_extra: dict | None = None) -> list[dict]:
    env = os.environ.copy()
    if env_extra:
        env.update(env_extra)
    payload = "\n".join(json.dumps(r) for r in requests) + "\n"
    proc = subprocess.run(
        [sys.executable, str(SERVER_PY)],
        input=payload,
        text=True,
        capture_output=True,
        cwd=cwd,
        env=env,
        timeout=30,
        check=True,
    )
    return [json.loads(line) for line in proc.stdout.splitlines() if line.strip()]


class DoDNSPluginMCPTest(unittest.TestCase):
    def test_initialize_list_and_zone_render(self):
        with tempfile.TemporaryDirectory() as tmp:
            resps = call_mcp([
                {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}},
                {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}},
                {"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {
                    "name": "zone_render",
                    "arguments": {"zone": "example.org", "ns_host": "ns1.example.org", "ns_ip": "198.51.100.10"},
                }},
            ], cwd=tmp)
            self.assertEqual(resps[0]["result"]["serverInfo"]["name"], "dodns-plugin-mcp")
            tool_names = [t["name"] for t in resps[1]["result"]["tools"]]
            for expected in ("query", "check_recursion", "zone_render", "zone_check", "record_add", "record_replace", "record_delete", "alidns_ensure"):
                self.assertIn(expected, tool_names)
            self.assertFalse(resps[2]["result"]["isError"])
            self.assertIn("IN SOA ns1.example.org.", resps[2]["result"]["content"][0]["text"])

    def test_zone_mutation_auto_rollback_and_path_secret_guards(self):
        with tempfile.TemporaryDirectory() as tmp:
            zone_content = (
                "$TTL 600\n"
                "@   IN SOA ns1.example.org. dns-admin.example.org. (\n"
                "        2026010101 ; serial\n"
                "        3600       ; refresh\n"
                "        900        ; retry\n"
                "        1209600    ; expire\n"
                "        600        ; minimum\n"
                ")\n"
                "    IN NS ns1.example.org.\n"
                "ns1 IN A 198.51.100.10\n"
            )
            zone_file = Path(tmp) / "db.example.org"
            zone_file.write_text(zone_content, encoding="utf-8")

            fail_check = Path(tmp) / "checkzone-fail.sh"
            fail_check.write_text("#!/bin/sh\necho syntax broken >&2\nexit 1\n", encoding="utf-8")
            fail_check.chmod(fail_check.stat().st_mode | stat.S_IXUSR)

            # 1. Failing checkzone MUST restore original zone bytes!
            r1 = call_mcp([{"jsonrpc": "2.0", "id": 10, "method": "tools/call", "params": {
                "name": "record_add",
                "arguments": {"zone": "example.org", "zone_file": "db.example.org", "name": "bad", "type": "A", "value": "203.0.113.9"},
            }}], cwd=tmp, env_extra={"DODNS_CHECKZONE_BIN": str(fail_check)})[0]
            self.assertTrue(r1["result"]["isError"])
            self.assertIn("automatically restored from backup", r1["result"]["content"][0]["text"])
            self.assertEqual(zone_file.read_text(encoding="utf-8"), zone_content)

            # 2. Path escape & secret argument MUST fail closed!
            r2 = call_mcp([
                {"jsonrpc": "2.0", "id": 11, "method": "tools/call", "params": {
                    "name": "record_add",
                    "arguments": {"zone_file": "../escape", "name": "a", "type": "A", "value": "203.0.113.9"},
                }},
                {"jsonrpc": "2.0", "id": 12, "method": "tools/call", "params": {
                    "name": "alidns_ensure",
                    "arguments": {"domain": "example.org", "rr": "app", "value": "203.0.113.9", "api_key": "canary"},
                }},
                {"jsonrpc": "2.0", "id": 13, "method": "tools/call", "params": {
                    "name": "alidns_ensure",
                    "arguments": {"domain": "example.org", "rr": "app", "type": "A", "value": "203.0.113.9", "dry_run": True},
                }},
            ], cwd=tmp)
            self.assertTrue(r2[0]["result"]["isError"])
            self.assertTrue(r2[1]["result"]["isError"])
            self.assertFalse(r2[2]["result"]["isError"])
            self.assertIn('"dryRun": true', r2[2]["result"]["content"][0]["text"])


if __name__ == "__main__":
    unittest.main()
