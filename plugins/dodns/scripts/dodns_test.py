#!/usr/bin/env python3
import datetime as dt
import struct
import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import dodns


class DoDNSTest(unittest.TestCase):
    def test_zone_serial_and_record_lifecycle(self):
        today = dt.date(2026, 11, 28)
        zone = dodns.render_minimal_zone("example.com", "ns1.example.com", "203.0.113.10")
        self.assertIn("ns1 IN A 203.0.113.10", zone)

        updated, s1 = dodns.append_record(zone, dodns.Record("app", "A", "203.0.113.20", 300), today=today)
        self.assertIn("app 300 IN A 203.0.113.20", updated)
        self.assertTrue(s1.startswith("20261128"))

        replaced, s2, count = dodns.replace_record(updated, dodns.Record("app", "A", "203.0.113.21", 600), today=today)
        self.assertEqual(count, 1)
        self.assertEqual(int(s2), int(s1) + 1)
        self.assertIn("app 600 IN A 203.0.113.21", replaced)

        deleted, s3, dcount = dodns.delete_record(replaced, "app", "A", today=today)
        self.assertEqual(dcount, 1)
        self.assertEqual(int(s3), int(s2) + 1)
        self.assertNotIn("203.0.113.21", deleted)

    def test_invalid_record_rejected(self):
        with self.assertRaises(ValueError):
            dodns.Record("app", "BADTYPE", "203.0.113.1").bind_line()
        with self.assertRaises(ValueError):
            dodns.Record("bad name", "A", "203.0.113.1").bind_line()

    def test_wire_query_and_parse_a_and_txt(self):
        q = dodns.build_query("app.example.com", "A", rd=True)
        txid = struct.unpack("!H", q[:2])[0]
        # Craft minimal response: QR=1, RD=1, RA=1 (0x8180), 1 question, 1 A answer
        question = q[12:]
        ans = b"\xc0\x0c" + struct.pack("!HHIH", 1, 1, 300, 4) + bytes([203, 0, 113, 42])
        resp = struct.pack("!HHHHHH", txid, 0x8180, 1, 1, 0, 0) + question + ans
        parsed = dodns.parse_response(resp)
        self.assertEqual(parsed["rcode"], 0)
        self.assertTrue(parsed["ra"])
        self.assertEqual(parsed["answers"][0]["value"], "203.0.113.42")


if __name__ == "__main__":
    unittest.main()
