#!/usr/bin/env python3
"""Create a consistent, private SQLite backup for Gateway migration rehearsal."""

import argparse
import hashlib
import json
import os
from contextlib import closing
from pathlib import Path
import sqlite3
import sys
import tempfile


def quoted(identifier):
    return '"' + identifier.replace('"', '""') + '"'


def open_read_only(path):
    db = sqlite3.connect(path.as_uri() + "?mode=ro", uri=True, timeout=5)
    db.execute("PRAGMA query_only=ON")
    return db


def inspect(db):
    if db.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
        raise ValueError("sqlite_integrity_check_failed")
    objects = db.execute(
        "SELECT type, name, tbl_name, sql FROM sqlite_schema "
        "WHERE substr(name, 1, 7) <> 'sqlite_' ORDER BY type, name"
    ).fetchall()
    counts = {
        name: db.execute("SELECT count(*) FROM " + quoted(name)).fetchone()[0]
        for kind, name, _, _ in objects if kind == "table"
    }
    sequence = {}
    if db.execute("SELECT 1 FROM sqlite_schema WHERE name='sqlite_sequence'").fetchone():
        sequence = dict(db.execute("SELECT name, seq FROM sqlite_sequence ORDER BY name"))
    canonical_schema = json.dumps(objects, ensure_ascii=True, separators=(",", ":"))
    return {
        "schema_sha256": hashlib.sha256(canonical_schema.encode()).hexdigest(),
        "object_counts": {kind: sum(obj[0] == kind for obj in objects) for kind in ("table", "index", "trigger", "view")},
        "table_rows": counts,
        "sqlite_sequence": sequence,
        "foreign_key_violations": sum(1 for _ in db.execute("PRAGMA foreign_key_check")),
    }


def sync_path(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def snapshot(source, destination):
    source = source.resolve(strict=True)
    destination = destination.parent.resolve(strict=True) / destination.name
    if not source.is_file() or source.stat().st_size == 0:
        raise ValueError("sqlite_source_not_nonempty_file")
    if os.path.lexists(destination):
        raise FileExistsError("snapshot_destination_exists")
    # A private directory keeps any SQLite sidecars private as well.
    with tempfile.TemporaryDirectory(prefix=".gateway-snapshot-", dir=destination.parent) as tmp:
        staging = Path(tmp) / "snapshot.db"
        staging.touch(mode=0o600, exist_ok=False)
        with closing(open_read_only(source)) as live, closing(sqlite3.connect(staging)) as copy:
            # Force establishment of a read snapshot. This sees committed WAL data
            # without restarting the backup every time the live Gateway writes.
            live.execute("BEGIN")
            live.execute("SELECT count(*) FROM sqlite_schema").fetchone()
            live.backup(copy, pages=2048, sleep=0.1)
            live.rollback()
            if copy.execute("PRAGMA journal_mode=DELETE").fetchone() != ("delete",):
                raise ValueError("snapshot_unexpected_journal_mode")
            copy.execute("PRAGMA query_only=ON")
            report = inspect(copy)
        sync_path(staging)
        # Atomic no-replace publish, even if another process created the path
        # between the preflight and completion of the backup.
        os.link(staging, destination)
        sync_path(destination.parent)
    report["snapshot_bytes"] = destination.stat().st_size
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, type=Path, help="SQLite source path (read-only)")
    parser.add_argument("--destination", required=True, type=Path, help="new backup file (must not exist)")
    args = parser.parse_args()
    try:
        report = snapshot(args.source, args.destination)
    except (OSError, sqlite3.Error, ValueError) as exc:
        # SQLite exceptions can contain names or database content. Never echo them.
        print("gateway_snapshot_failed: " + type(exc).__name__, file=sys.stderr)
        return 1
    print(json.dumps(report, ensure_ascii=True, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
