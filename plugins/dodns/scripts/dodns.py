#!/usr/bin/env python3
"""DoDNS pure-Python stdlib DNS wire prober and safe BIND zone helper."""
from __future__ import annotations

import argparse
import datetime as _dt
import json
import os
import random
import re
import shutil
import socket
import struct
import sys
import time
from dataclasses import dataclass

SUPPORTED_RECORD_TYPES = {"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SOA"}
QTYPE = {"A": 1, "NS": 2, "CNAME": 5, "SOA": 6, "PTR": 12, "MX": 15, "TXT": 16, "AAAA": 28}
TYPE_NAME = {v: k for k, v in QTYPE.items()}


@dataclass(frozen=True)
class Record:
    name: str
    type: str
    value: str
    ttl: int | None = None

    def bind_line(self) -> str:
        rtype = self.type.upper()
        if rtype not in SUPPORTED_RECORD_TYPES:
            raise ValueError(f"unsupported record type: {rtype}")
        if not self.name or re.search(r"[\s;()]", self.name):
            raise ValueError(f"invalid record name: {self.name!r}")
        if not self.value or "\n" in self.value or "\r" in self.value:
            raise ValueError("invalid record value")
        ttl = f"{self.ttl} " if self.ttl else ""
        return f"{self.name} {ttl}IN {rtype} {self.value}".strip()


def encode_name(name: str) -> bytes:
    out = b""
    for part in name.strip().rstrip(".").split("."):
        if not part:
            continue
        raw = part.encode("ascii")
        if len(raw) > 63:
            raise ValueError(f"DNS label too long: {part}")
        out += bytes([len(raw)]) + raw
    return out + b"\0"


def read_name(data: bytes, offset: int, depth: int = 0) -> tuple[str, int]:
    if depth > 12:
        return "", offset
    labels: list[str] = []
    jumped = False
    orig_next = offset
    while offset < len(data):
        length = data[offset]
        if length == 0:
            offset += 1
            break
        if (length & 0xC0) == 0xC0:
            if offset + 1 >= len(data):
                break
            ptr = ((length & 0x3F) << 8) | data[offset + 1]
            if not jumped:
                orig_next = offset + 2
                jumped = True
            sub, _ = read_name(data, ptr, depth + 1)
            if sub:
                labels.append(sub)
            break
        offset += 1
        labels.append(data[offset : offset + length].decode("ascii", errors="replace"))
        offset += length
    return ".".join(labels), (orig_next if jumped else offset)


def decode_rdata(data: bytes, rtype: int, offset: int, rdlen: int) -> str:
    rdata = data[offset : offset + rdlen]
    if rtype == 1 and rdlen == 4:
        return ".".join(str(b) for b in rdata)
    if rtype == 28 and rdlen == 16:
        return socket.inet_ntop(socket.AF_INET6, rdata)
    if rtype in (2, 5, 12):
        val, _ = read_name(data, offset)
        return val
    if rtype == 15 and rdlen >= 3:
        pref = struct.unpack("!H", rdata[:2])[0]
        exch, _ = read_name(data, offset + 2)
        return f"{pref} {exch}"
    if rtype == 16:
        pos, chunks = 0, []
        while pos < len(rdata):
            ln = rdata[pos]
            pos += 1
            chunks.append(rdata[pos : pos + ln].decode("utf-8", errors="replace"))
            pos += ln
        return "".join(chunks)
    if rtype == 6:
        mname, nxt = read_name(data, offset)
        rname, nxt = read_name(data, nxt)
        if nxt + 20 <= len(data):
            serial = struct.unpack("!I", data[nxt : nxt + 4])[0]
            return f"{mname} {rname} serial={serial}"
        return f"{mname} {rname}"
    return rdata.hex()


def build_query(name: str, qtype: str = "A", rd: bool = True) -> bytes:
    qtype = qtype.upper()
    if qtype not in QTYPE:
        raise ValueError(f"unsupported query type: {qtype}")
    txid = random.randrange(65536)
    flags = 0x0100 if rd else 0x0000
    pkt = struct.pack("!HHHHHH", txid, flags, 1, 0, 0, 0)
    pkt += encode_name(name)
    pkt += struct.pack("!HH", QTYPE[qtype], 1)
    return pkt


def parse_response(data: bytes) -> dict:
    if len(data) < 12:
        raise ValueError("short DNS response")
    txid, flags, qdcount, ancount, nscount, arcount = struct.unpack("!HHHHHH", data[:12])
    offset = 12
    for _ in range(qdcount):
        _, offset = read_name(data, offset)
        offset += 4
    answers = []
    for _ in range(ancount):
        _, offset = read_name(data, offset)
        rtype, rclass, ttl, rdlen = struct.unpack("!HHIH", data[offset : offset + 10])
        offset += 10
        val = decode_rdata(data, rtype, offset, rdlen)
        offset += rdlen
        answers.append({"type": TYPE_NAME.get(rtype, str(rtype)), "class": rclass, "ttl": ttl, "value": val})
    return {
        "id": txid,
        "rcode": flags & 0xF,
        "aa": bool(flags & 0x0400),
        "ra": bool(flags & 0x0080),
        "answer_count": ancount,
        "authority_count": nscount,
        "additional_count": arcount,
        "answers": answers,
    }


def raw_udp_query(server: str, name: str, qtype: str = "A", port: int = 53, rd: bool = True, timeout: float = 3.0) -> dict:
    t0 = time.time()
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.settimeout(timeout)
    try:
        sock.sendto(build_query(name, qtype, rd=rd), (server, port))
        data, addr = sock.recvfrom(4096)
        parsed = parse_response(data)
        parsed["server"] = f"{addr[0]}:{addr[1]}"
        parsed["latency_ms"] = round((time.time() - t0) * 1000.0, 1)
        return parsed
    finally:
        sock.close()


def next_serial(current: str | None = None, today: _dt.date | None = None) -> str:
    today = today or _dt.date.today()
    prefix = today.strftime("%Y%m%d")
    if current and current.startswith(prefix) and current.isdigit():
        return str(int(current) + 1)
    return f"{prefix}01"


def find_soa_serial(zone_text: str) -> str | None:
    match = re.search(r"\(\s*\n?\s*(\d{10})\s*;?\s*serial", zone_text, flags=re.IGNORECASE)
    if match:
        return match.group(1)
    match = re.search(r"SOA[^\n(]*\(\s*(\d{10})", zone_text, flags=re.IGNORECASE)
    return match.group(1) if match else None


def bump_soa_serial(zone_text: str, today: _dt.date | None = None) -> tuple[str, str]:
    current = find_soa_serial(zone_text)
    if not current:
        raise ValueError("SOA serial not found")
    new_serial = next_serial(current, today)
    return zone_text.replace(current, new_serial, 1), new_serial


def append_record(zone_text: str, record: Record, today: _dt.date | None = None) -> tuple[str, str]:
    updated, serial = bump_soa_serial(zone_text, today)
    if not updated.endswith("\n"):
        updated += "\n"
    return updated + record.bind_line() + "\n", serial


def replace_record(zone_text: str, record: Record, today: _dt.date | None = None) -> tuple[str, str, int]:
    rtype = record.type.upper()
    pattern = re.compile(rf"^({re.escape(record.name)}\s+(?:\d+\s+)?IN\s+{rtype}\s+).*$", re.MULTILINE)
    updated, count = pattern.subn(record.bind_line(), zone_text)
    if count == 0:
        raise ValueError(f"record not found: {record.name} {rtype}")
    updated, serial = bump_soa_serial(updated, today)
    return updated, serial, count


def delete_record(zone_text: str, name: str, rtype: str, today: _dt.date | None = None) -> tuple[str, str, int]:
    pattern = re.compile(rf"^{re.escape(name)}\s+(?:\d+\s+)?IN\s+{re.escape(rtype.upper())}\s+.*\n?", re.MULTILINE)
    updated, count = pattern.subn("", zone_text)
    if count == 0:
        raise ValueError(f"record not found: {name} {rtype}")
    updated, serial = bump_soa_serial(updated, today)
    return updated, serial, count


def render_minimal_zone(zone: str, ns_host: str, ns_ip: str, records: list[Record] | None = None) -> str:
    serial = next_serial()
    ns_label = ns_host.rstrip(".")
    suffix = "." + zone.rstrip(".")
    if ns_label.endswith(suffix):
        ns_label = ns_label[: -len(suffix)]
    lines = [
        "$TTL 600",
        f"@   IN SOA {ns_host.rstrip('.')}. dns-admin.{zone.rstrip('.')}. (",
        f"        {serial} ; serial",
        "        3600       ; refresh",
        "        900        ; retry",
        "        1209600    ; expire",
        "        600        ; minimum",
        ")",
        f"    IN NS {ns_host.rstrip('.')}.",
        f"{ns_label} IN A {ns_ip}",
    ]
    for record in records or []:
        lines.append(record.bind_line())
    return "\n".join(lines) + "\n"


def _write_with_backup(path: str, content: str) -> str:
    stamp = _dt.datetime.now(_dt.timezone.utc).strftime("%Y%m%d-%H%M%S")
    backup = f"{path}.bak.{stamp}"
    shutil.copy2(path, backup)
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)
    return backup


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="DoDNS pure-Python DNS query & BIND zone tool")
    sub = parser.add_subparsers(dest="command", required=True)

    p_q = sub.add_parser("query", help="Send a raw UDP DNS query")
    p_q.add_argument("--server", required=True)
    p_q.add_argument("--port", type=int, default=53)
    p_q.add_argument("--name", required=True)
    p_q.add_argument("--type", default="A", choices=sorted(QTYPE))
    p_q.add_argument("--norecurse", action="store_true")
    p_q.add_argument("--timeout", type=float, default=3.0)

    p_rec = sub.add_parser("check-recursion", help="Check whether a DNS server answers recursive queries")
    p_rec.add_argument("--server", required=True)
    p_rec.add_argument("--port", type=int, default=53)
    p_rec.add_argument("--probe-name", default="example.com")
    p_rec.add_argument("--timeout", type=float, default=2.5)

    p_ren = sub.add_parser("zone-render", help="Render a minimal BIND zone file")
    p_ren.add_argument("--zone", required=True)
    p_ren.add_argument("--ns-host", required=True)
    p_ren.add_argument("--ns-ip", required=True)

    for cmd in ("record-add", "record-replace"):
        p = sub.add_parser(cmd, help=f"{cmd} in a BIND zone file (dry-run by default)")
        p.add_argument("--zone-file", required=True)
        p.add_argument("--name", required=True)
        p.add_argument("--type", required=True, choices=sorted(SUPPORTED_RECORD_TYPES))
        p.add_argument("--value", required=True)
        p.add_argument("--ttl", type=int, default=None)
        p.add_argument("--write", action="store_true")

    p_del = sub.add_parser("record-delete", help="Delete a record from a BIND zone file (dry-run by default)")
    p_del.add_argument("--zone-file", required=True)
    p_del.add_argument("--name", required=True)
    p_del.add_argument("--type", required=True, choices=sorted(SUPPORTED_RECORD_TYPES))
    p_del.add_argument("--write", action="store_true")

    args = parser.parse_args(argv)
    if args.command == "query":
        res = raw_udp_query(args.server, args.name, args.type, args.port, rd=not args.norecurse, timeout=args.timeout)
        print(json.dumps(res, ensure_ascii=False, indent=2))
        return 0 if res["rcode"] == 0 else 1
    if args.command == "check-recursion":
        res = raw_udp_query(args.server, args.probe_name, "A", args.port, rd=True, timeout=args.timeout)
        out = {"server": res["server"], "open_recursion": bool(res["ra"] and res["rcode"] == 0), "ra": res["ra"], "rcode": res["rcode"], "answers": res["answer_count"]}
        print(json.dumps(out, ensure_ascii=False, indent=2))
        return 0
    if args.command == "zone-render":
        print(render_minimal_zone(args.zone, args.ns_host, args.ns_ip), end="")
        return 0
    if args.command in ("record-add", "record-replace", "record-delete"):
        text = open(args.zone_file, "r", encoding="utf-8").read()
        if args.command == "record-add":
            updated, serial = append_record(text, Record(args.name, args.type, args.value, args.ttl))
            changed = 1
        elif args.command == "record-replace":
            updated, serial, changed = replace_record(text, Record(args.name, args.type, args.value, args.ttl))
        else:
            updated, serial, changed = delete_record(text, args.name, args.type)
        backup = _write_with_backup(args.zone_file, updated) if args.write else ""
        print(json.dumps({"zone_file": os.path.abspath(args.zone_file), "written": bool(args.write), "backup": backup, "serial": serial, "changed": changed}, ensure_ascii=False, indent=2))
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main())
