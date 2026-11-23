#!/usr/bin/env python3
"""SF-08 鲁棒性场景校验器。

对鲁棒性夹具产出的单条巡检产物做通用协议断言 + 场景专项断言（spec §4.1 / §5）：
  - 文件 <= max-bytes（默认 256 KiB）、最后一个 json 代码块可解析
  - 顶层 status/summary 齐备，status 仅 pass|warning|critical（禁 skipped）
  - 无 userinfo 形式凭据
  - 可选 --spec 场景断言：status=、check=、detail=、mount=、forbid=、
    minmounts=、minchecks=、degraded=1、max=、target=1
判定「是否降级 + 降级理由是否在 checks[].detail」并打印一行结论。
用法：robust_check.py <产物文件> <slug> --spec "status=warning,check=host.disk"
退出码：0 通过；1 失败（打印 ROBUST_CHECK_FAIL 原因）。
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
    print("ROBUST_CHECK_FAIL %s: %s" % (slug, reason))
    sys.exit(1)


def parse_spec(spec):
    out = {}
    for part in spec.split(","):
        part = part.strip()
        if not part:
            continue
        if "=" in part:
            k, v = part.split("=", 1)
        else:
            k, v = part, "1"
        out[k.strip()] = v
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("path")
    ap.add_argument("slug")
    ap.add_argument("--spec", default="")
    args = ap.parse_args()
    spec = parse_spec(args.spec)

    limit = int(spec.get("max", 262144))
    size = os.path.getsize(args.path)
    if size > limit:
        fail(args.slug, "单条产物 %d 字节超过上限 %d" % (size, limit))
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
    for f in ("status", "summary"):
        if f not in payload:
            fail(args.slug, "载荷缺少必需字段 %s" % f)
    if payload["status"] not in STATUS:
        fail(args.slug, "status=%r 不在 %s" % (payload["status"], sorted(STATUS)))
    if not isinstance(payload["summary"], str) or not payload["summary"].strip():
        fail(args.slug, "summary 为空")
    text = json.dumps(payload, ensure_ascii=False)
    if "skipped" in text:
        fail(args.slug, "载荷仍含 spec 4.1 已取消的 status=skipped")
    if USERINFO.search(raw) or USERINFO.search(text):
        fail(args.slug, "输出中出现 userinfo 形式的凭据")

    checks = payload.get("checks", [])
    if not isinstance(checks, list):
        fail(args.slug, "checks 不是数组")
    if len(checks) < int(spec.get("minchecks", 1)):
        fail(args.slug, "checks 数量不足（%d < %s）" % (len(checks), spec.get("minchecks")))
    mounts = payload.get("mounts", [])
    if not isinstance(mounts, list):
        mounts = []
    mount_paths = [str(m.get("mount", "")) for m in mounts if isinstance(m, dict)]

    if spec.get("target") == "1":
        tgt = payload.get("target")
        if not isinstance(tgt, dict) or "cluster" not in tgt or "instance" not in tgt:
            fail(args.slug, "target 字段缺失或结构非法（需 cluster/instance 两键）")
    if "status" in spec and payload["status"] != spec["status"]:
        fail(args.slug, "status=%r 期望 %r" % (payload["status"], spec["status"]))
    if "check" in spec:
        ids = [str(c.get("id", "")) for c in checks if isinstance(c, dict)]
        if spec["check"] not in ids:
            fail(args.slug, "缺少 check id=%s（现有 %s）" % (spec["check"], ids))
    if "detail" in spec:
        details = " ".join(str(c.get("detail", "")) for c in checks if isinstance(c, dict))
        if spec["detail"] not in details:
            fail(args.slug, "checks[].detail 未包含 %r" % spec["detail"])
    if "mount" in spec and spec["mount"] not in mount_paths:
        fail(args.slug, "mounts 未包含 %r（现有 %s）" % (spec["mount"], mount_paths))
    if "minmounts" in spec and len(mounts) < int(spec["minmounts"]):
        fail(args.slug, "mounts 数量不足（%d < %s）" % (len(mounts), spec["minmounts"]))
    if "forbid" in spec and spec["forbid"] and spec["forbid"] in raw:
        fail(args.slug, "输出中出现禁止子串 %r" % spec["forbid"])

    bad = [c for c in checks if isinstance(c, dict) and c.get("status") != "pass"]
    degraded = payload["status"] != "pass" or bool(bad)
    reasons = [str(c.get("detail", "")).strip() for c in bad if str(c.get("detail", "")).strip()]
    reason = " | ".join(reasons)[:160] if reasons else "(无非 pass check 说明)"
    if spec.get("degraded") == "1":
        if payload["status"] == "pass":
            fail(args.slug, "期望降级但 status=pass")
        if not reasons:
            fail(args.slug, "已降级但没有任何 checks[].detail 说明理由")

    print("ROBUST_CHECK %s status=%s degraded=%s reason=%s checks=%d mounts=%d bytes=%d" % (
        args.slug, payload["status"], "yes" if degraded else "no", reason,
        len(checks), len(mounts), size))


if __name__ == "__main__":
    main()
