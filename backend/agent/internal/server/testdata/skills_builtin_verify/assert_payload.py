#!/usr/bin/env python3
"""SB-01 载荷协议校验器。

解析巡检脚本 markdown 输出中的 json 代码块，断言符合
backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md 第 4.1 节输出协议与红线：
  - status/summary/metrics/checks 齐备，status 仅允许 pass|warning|critical（禁 skipped）
  - metrics/checks 每项字段与枚举合法
  - 单条产物 <= 256 KiB（spec L28）
  - 输出中不得出现 userinfo 形式的凭据（scheme://user:pass@host）
用法：assert_payload.py <输出文件> <slug> [--expect-status S] [--require-metric K]... [--forbid-substring T]... [--require-target]
退出码：0 通过；1 失败（打印 ASSERT_FAIL 原因）。
"""
import argparse
import json
import os
import re
import sys

STATUS = {"pass", "warning", "critical"}
FENCE = re.compile(r"```json[ \t\r\n]*(.*?)[ \t\r\n]*```", re.DOTALL)
USERINFO = re.compile(r"[a-zA-Z][a-zA-Z0-9+.-]*://[^/@ \t\r\n]+:[^/@ \t\r\n]+@")


def fail(slug, reason):
    print("ASSERT_FAIL %s: %s" % (slug, reason))
    sys.exit(1)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("path")
    ap.add_argument("slug")
    ap.add_argument("--expect-status", default="")
    ap.add_argument("--require-metric", action="append", default=[])
    ap.add_argument("--forbid-substring", action="append", default=[])
    ap.add_argument("--min-checks", type=int, default=1)
    ap.add_argument("--require-target", action="store_true")
    args = ap.parse_args()

    size = os.path.getsize(args.path)
    if size > 256 * 1024:
        fail(args.slug, "单条产物 %d 字节超过 256 KiB 上限" % size)
    raw = open(args.path, encoding="utf-8", errors="replace").read()

    blocks = FENCE.findall(raw)
    if not blocks:
        fail(args.slug, "输出中未找到 json 代码块")
    try:
        payload = json.loads(blocks[-1])
    except Exception as e:
        fail(args.slug, "json 代码块无法解析: %s" % e)
    if not isinstance(payload, dict):
        fail(args.slug, "载荷顶层不是对象")

    for field in ("status", "summary", "metrics", "checks"):
        if field not in payload:
            fail(args.slug, "载荷缺少必需字段 %s" % field)
    if payload["status"] not in STATUS:
        fail(args.slug, "status=%r 不在 %s" % (payload["status"], sorted(STATUS)))
    if not isinstance(payload["summary"], str) or not payload["summary"].strip():
        fail(args.slug, "summary 为空")
    if not isinstance(payload["metrics"], list):
        fail(args.slug, "metrics 不是数组")
    for i, m in enumerate(payload["metrics"]):
        for f in ("key", "label", "value", "unit", "status"):
            if f not in m:
                fail(args.slug, "metrics[%d] 缺少 %s" % (i, f))
        if m["status"] not in STATUS:
            fail(args.slug, "metrics[%d].status=%r 非法" % (i, m["status"]))
        if not re.match(r"^[a-z][a-z0-9_]*$", str(m["key"])):
            fail(args.slug, "metrics[%d].key=%r 命名非法" % (i, m["key"]))
    checks = payload["checks"]
    if not isinstance(checks, list) or len(checks) < args.min_checks:
        fail(args.slug, "checks 数量不足（%s < %d）" % (len(checks) if isinstance(checks, list) else -1, args.min_checks))
    for i, c in enumerate(checks):
        for f in ("id", "name", "category", "status", "value", "detail"):
            if f not in c:
                fail(args.slug, "checks[%d] 缺少 %s" % (i, f))
        if c["status"] not in STATUS:
            fail(args.slug, "checks[%d].status=%r 非法" % (i, c["status"]))

    if args.require_target:
        tgt = payload.get("target")
        if not isinstance(tgt, dict) or "cluster" not in tgt or "instance" not in tgt:
            fail(args.slug, "target 字段缺失或结构非法（需 cluster/instance 两键）")
        for k in ("cluster", "instance"):
            if tgt[k] is not None and not isinstance(tgt[k], str):
                fail(args.slug, "target.%s 既不是 null 也不是字符串" % k)

    text = json.dumps(payload, ensure_ascii=False)
    if "skipped" in text:
        fail(args.slug, "载荷仍含 spec 4.1 已取消的 status=skipped")
    if USERINFO.search(raw) or USERINFO.search(text):
        fail(args.slug, "输出中出现 userinfo 形式的凭据")
    for s in args.forbid_substring:
        if s and s in raw:
            fail(args.slug, "输出中出现禁止子串 %r" % s)
    if args.expect_status and payload["status"] != args.expect_status:
        fail(args.slug, "status=%r 期望 %r" % (payload["status"], args.expect_status))
    keys = set(m["key"] for m in payload["metrics"])
    for k in args.require_metric:
        if k not in keys:
            fail(args.slug, "缺少指标 %s（现有 %s）" % (k, sorted(keys)))

    print("ASSERT_OK %s status=%s metrics=%d checks=%d bytes=%d" % (
        args.slug, payload["status"], len(payload["metrics"]), len(checks), size))


if __name__ == "__main__":
    main()
