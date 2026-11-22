import argparse
import sqlite3
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))
import asset_monitor_collector as collector


class DiskIOTest(unittest.TestCase):
    def test_collects_diskstats_and_does_not_fill_gaps(self):
        before = (
            "259 0 nvme0n1 10 0 100 35 20 0 200 50 0 0 0\n"
            "8 0 sda 10 0 100 35 20 0 200 50 0 0 0\n"
            "8 16 sdb 10 0 100 35 20 0 200 50 0 0 0\n"
        )
        after = (
            "259 0 nvme0n1 14 0 120 43 20 0 200 50 0 0 0\n"
            "8 0 sda 0 0 0 0 0 0 0 0 0 0 0\n"
            "8 16 sdb 10 0 100 35 22 0 232 60 0 0 0\n"
        )
        args = argparse.Namespace(cluster="test", instance="local", pod_namespace=None,
                                  type="machine", asset_id="srv-1", namespace="", host=False,
                                  pid=None, nginx_status=None, interface=[], mount=[], gpu=[],
                                  disk=["nvme0n1", "sda", "sdb", "vdb"])
        with patch.object(Path, "read_text", side_effect=[before, after]), \
                patch.object(collector.time, "monotonic", side_effect=[20, 22]), \
                patch.object(collector.time, "sleep"):
            payload = collector.collect(args)
        result = {(p["device_id"], p["metric"]): (p["status"], p["value"]) for p in payload["samples"]}
        self.assertEqual(len(result), 24)
        self.assertEqual(result["nvme0n1", "disk_read_iops"], ("ok", 2))
        self.assertEqual(result["nvme0n1", "disk_read_bytes_per_second"], ("ok", 5120))
        self.assertEqual(result["nvme0n1", "disk_read_latency_ms"], ("ok", 2))
        self.assertEqual(result["nvme0n1", "disk_write_iops"], ("ok", 0))
        self.assertEqual(result["nvme0n1", "disk_write_bytes_per_second"], ("ok", 0))
        self.assertEqual(result["nvme0n1", "disk_write_latency_ms"], ("missing", None))
        self.assertEqual(result["sdb", "disk_write_iops"], ("ok", 1))
        self.assertEqual(result["sdb", "disk_write_bytes_per_second"], ("ok", 8192))
        self.assertEqual(result["sdb", "disk_write_latency_ms"], ("ok", 5))
        for device in ("sda", "vdb"):
            for metric, _ in collector.DISK_METRICS:
                self.assertEqual(result[device, metric], ("missing", None))

    def test_migration_preserves_series_references(self):
        migrations = Path(__file__).resolve().parents[1] / "docs/migrations"
        with sqlite3.connect(":memory:") as db:
            db.executescript((migrations / "asset_monitor.sql").read_text())
            db.executescript((migrations / "asset_monitor_devices.sql").read_text())
            db.executescript("""
                INSERT INTO asset_monitor_series VALUES (42,'test','local','','machine','srv-1','exporter','network','eth0');
                INSERT INTO asset_monitor_samples VALUES (42,100,'ok',2);
                INSERT INTO asset_monitor_rules VALUES (42,1,1,0,0);
                INSERT INTO asset_monitor_alerts VALUES (42,100,1,1,NULL,'open');
            """)
            db.executescript((migrations / "asset_monitor_disk_io.sql").read_text())
            db.execute("INSERT INTO asset_monitor_series VALUES (43,'test','local','','machine','srv-1','exporter','disk_read_iops','nvme0n1')")
            for table in ("samples", "rules", "alerts"):
                self.assertEqual(db.execute(f"SELECT series_id FROM asset_monitor_{table}").fetchall(), [(42,)])
            self.assertEqual(db.execute("PRAGMA foreign_key_check").fetchall(), [])
