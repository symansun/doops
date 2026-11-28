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
                                  pid=None, nginx_status=None, http_probe=None, interface=[], mount=[], gpu=[],
                                  disk=["nvme0n1", "sda", "sdb", "vdb"], containers=False)
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
            db.execute("INSERT INTO asset_monitor_series VALUES (44,'test','local','','machine','srv-1','agent_host','disk_read_iops','vda')")
            db.execute("INSERT INTO asset_monitor_series VALUES (45,'test','local','','container','web-nginx','container_stats','memory_limit','')")
            for table in ("samples", "rules", "alerts"):
                self.assertEqual(db.execute(f"SELECT series_id FROM asset_monitor_{table}").fetchall(), [(42,)])
            self.assertEqual(db.execute("PRAGMA foreign_key_check").fetchall(), [])

    def test_collects_container_stats(self):
        sample_out = (
            '{"Name":"/app-api","CPUPerc":"150.00%","MemUsage":"256MiB / 1GiB"}\n'
            '{"Name":"redis","CPUPerc":"12.5%","MemUsage":"64MiB / --"}\n'
        )
        args = argparse.Namespace(cluster="test", instance="local", pod_namespace=None,
                                  type="machine", asset_id="", namespace="", host=False,
                                  pid=None, nginx_status=None, interface=[], mount=[], gpu=[],
                                  disk=[], containers=True)
        with patch.object(collector.subprocess, "check_output", return_value=sample_out):
            payload = collector.collect(args)
        got = {(p["asset_id"], p["metric"]): (p["status"], p["value"]) for p in payload["samples"]}
        self.assertEqual(got["app-api", "cpu"], ("ok", 1.5))
        self.assertEqual(got["app-api", "memory"], ("ok", 256 * 1048576))
        self.assertEqual(got["app-api", "memory_limit"], ("ok", 1073741824))
        self.assertEqual(got["redis", "memory_limit"], ("missing", None))

    def test_collects_nginx_active_connections_and_http_probe(self):
        args = argparse.Namespace(cluster="test", instance="local", pod_namespace=None,
                                  type="app", asset_id="portal", namespace="", host=False,
                                  pid=None, nginx_status=collector.urlsplit("http://127.0.0.1/nginx_status"),
                                  http_probe=collector.urlsplit("https://portal.example/healthz"),
                                  interface=[], mount=[], gpu=[], disk=[], containers=False)
        with patch.object(collector, "nginx_stats", side_effect=[(12, 1000), (15, 1020), (15, 1020)]), \
                patch.object(collector, "http_probe", return_value=(18.5, 0.0)), \
                patch.object(collector.time, "monotonic", side_effect=[10.0, 12.0, 12.0, 12.1]), \
                patch.object(collector.time, "sleep"):
            payload = collector.collect(args)
        got = {p["metric"]: (p["status"], p["value"]) for p in payload["samples"]}
        self.assertEqual(got["requests"], ("ok", 10.0))
        self.assertEqual(got["active_connections"], ("ok", 15))
        self.assertEqual(got["response_time_ms"], ("ok", 18.5))
        self.assertEqual(got["error_rate"], ("ok", 0.0))
