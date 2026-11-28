#!/usr/bin/env python3
"""切版核对清单：把两个 tag 之间的 feat/fix 提交打印成一张可勾选清单。

背景：0.3.38 里带进了终端 pty 修复，
但 CHANGELOG.md 的 `## [0.3.38]` 段里一个字都没提——代码随版本走、更新内容没跟上。
「这条改动用户看得见吗」无法机器判定，所以本脚本**不是门禁**：除了 git 调用失败、
tag 不存在、CHANGELOG 找不到该版本段这几种硬错误（退出码 2），退出码恒为 0。

用法:
    python3 backend/scripts/changelog-cut-checklist.py <上一个 tag> <候选 tag>

上一个 tag 必须能解析到提交；候选 tag 允许还不存在（还没打 tag），此时回落到 HEAD
并明确打印 `candidate tag does not exist, using HEAD`。
"""

import re
import subprocess
import sys
from pathlib import Path

EXIT_OK = 0
EXIT_HARD = 2

# Conventional Commits: type(scope)!: subject —— scope 与 breaking 标记都可选。
COMMIT_RE = re.compile(
    r"^(?P<type>[A-Za-z]+)(?:\([^()]*\))?(?P<breaking>!)?:\s*(?P<subject>.+)$"
)
SECTION_RE = re.compile(r"^##\s+\[(?P<version>[^\]]+)\]")
CATEGORY_RE = re.compile(r"^###\s+(?P<name>.+?)\s*$")
ENTRY_RE = re.compile(r"^-\s+\S")
KEEP_TYPES = {"feat", "fix"}
FILES_COLUMN = 3


def die(message):
    print(f"changelog-cut-checklist: {message}", file=sys.stderr)
    return EXIT_HARD


def git(*args):
    return subprocess.run(["git", *args], capture_output=True, text=True)


def resolve_commit(revish):
    proc = git("rev-parse", "--verify", "--quiet", f"{revish}^{{commit}}")
    if proc.returncode != 0:
        return None
    return proc.stdout.strip()


def files_touched(sha):
    proc = git("diff-tree", "--no-commit-id", "--name-only", "-r", "--root", sha)
    if proc.returncode != 0:
        return None
    return len([line for line in proc.stdout.splitlines() if line.strip()])


def print_changelog_section(root, version):
    changelog = root / "CHANGELOG.md"
    if not changelog.is_file():
        return die(f"{changelog} does not exist")

    lines = changelog.read_text(encoding="utf-8").splitlines()
    start = None
    for index, line in enumerate(lines):
        match = SECTION_RE.match(line)
        if match and match.group("version").strip() == version:
            start = index
            break
    if start is None:
        return die(
            f"CHANGELOG.md has no `## [{version}]` section; add the version heading "
            "and its entries before cutting the release"
        )

    end = len(lines)
    for index in range(start + 1, len(lines)):
        if SECTION_RE.match(lines[index]):
            end = index
            break

    body = lines[start + 1:end]
    entries = sum(1 for line in body if ENTRY_RE.match(line))
    categories = [CATEGORY_RE.match(line).group("name") for line in body if CATEGORY_RE.match(line)]

    suffix = f" (categories: {', '.join(categories)})" if categories else ""
    print(f"CHANGELOG: section [{version}] at line {start + 1}, entries: {entries}{suffix}")
    if entries == 0:
        print("  note: 该版本段没有条目——若本版确有用户可见变更，这就是 D44 那类漏写。")
    return EXIT_OK


def main(argv):
    arguments = argv[1:]
    if any(argument in ("-h", "--help") for argument in arguments):
        print(__doc__.strip())
        return EXIT_OK
    if len(arguments) != 2:
        return die("expected exactly two tags: <previous-tag> <candidate-tag> (see --help)")
    previous_tag, candidate_tag = arguments

    top = git("rev-parse", "--show-toplevel")
    if top.returncode != 0:
        return die("not inside a git work tree")
    root = Path(top.stdout.strip())

    previous_commit = resolve_commit(previous_tag)
    if previous_commit is None:
        return die(f"previous tag {previous_tag!r} does not exist (git rev-parse failed)")
    candidate_commit = resolve_commit(candidate_tag)
    using_head = candidate_commit is None
    if using_head:
        candidate_commit = resolve_commit("HEAD")
        if candidate_commit is None:
            return die("candidate tag does not exist and HEAD is not resolvable")
        print("candidate tag does not exist, using HEAD")

    log = git("log", "--no-merges", "--format=%H%x09%s", f"{previous_commit}..{candidate_commit}")
    if log.returncode != 0:
        return die(f"git log failed: {log.stderr.strip()}")

    rows = []
    ignored = 0
    for line in log.stdout.splitlines():
        if not line.strip():
            continue
        sha, _, subject = line.partition("\t")
        match = COMMIT_RE.match(subject)
        if not match or match.group("type").lower() not in KEEP_TYPES:
            ignored += 1
            continue
        count = files_touched(sha)
        if count is None:
            return die(f"git diff-tree failed for {sha[:8]}")
        kind = match.group("type").lower()
        if match.group("breaking"):
            kind += "!"
        rows.append((sha[:8], kind, count, match.group("subject")))

    target = "HEAD" if using_head else candidate_tag
    print(f"切版核对清单: {previous_tag} -> {target}")
    print(f"range: {previous_commit}..{candidate_commit} (不含 {previous_tag} 自身)")
    print()
    if not rows:
        print("(no feat/fix commits in range)")
    for sha8, kind, count, subject in rows:
        print(f"[ ] {sha8}  {kind:<6} {count:>{FILES_COLUMN}} files  {subject}")
    print()
    print(f"feat/fix 待核对: {len(rows)} 条；非 feat/fix 提交（已忽略）: {ignored} 条")
    print("每条二选一后勾选：写进该版本条目 / 属内部改动无需写。本脚本只打印不判定（D44）。")

    if using_head:
        print("candidate tag does not exist, so the CHANGELOG section cannot be located; skipping.")
        return EXIT_OK

    version = candidate_tag[1:] if candidate_tag[:1] in ("v", "V") else candidate_tag
    return print_changelog_section(root, version)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
