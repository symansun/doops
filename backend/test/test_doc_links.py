"""Structural checks for the operator Skill and its installed docs.

These tests lock links and size, not wording. Rules are stated once in the document that
owns the topic (see Topic Ownership in AGENTS.md), so rephrasing a rule must not fail CI.
"""

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
BACKEND = ROOT / "backend"
SKILL = BACKEND / "skills" / "SKILL.md"
DOCS = BACKEND / "docs"
AGENT_RULES = ROOT / "AGENTS.md"

# Guards against the Skill growing back into a full manual. Phase-4 target is 10KB.
SKILL_MAX_BYTES = 40_000

# `docs/NAME.md` mentions in prose or code spans, and relative markdown link targets.
DOC_MENTION = re.compile(r"(?<![\w/.-])docs/([A-Za-z0-9_./-]+\.md)")
MD_LINK = re.compile(r"\]\(([^)#\s]+\.md)(?:#[^)]*)?\)")


def _missing_links(path: Path):
    text = path.read_text(encoding="utf-8")
    # Installed layout puts docs/ beside SKILL.md; in the source tree that is backend/.
    base = BACKEND if path == SKILL else path.parent
    missing = []
    for name in DOC_MENTION.findall(text):
        # Paths are relative to the installed Skill root, where docs/ sits beside SKILL.md.
        if not (DOCS / name).is_file():
            missing.append(f"{path.relative_to(ROOT)}: docs/{name}")
    for target in MD_LINK.findall(text):
        if target.startswith(("http://", "https://")):
            continue
        if not (base / target).resolve().is_file():
            missing.append(f"{path.relative_to(ROOT)}: {target}")
    return missing


def test_skill_and_installed_docs_only_link_to_existing_docs():
    paths = [SKILL, *sorted(DOCS.glob("*.md"))]
    missing = [item for path in paths for item in _missing_links(path)]
    assert missing == []


def test_topic_owner_documents_exist():
    rules = AGENT_RULES.read_text(encoding="utf-8")
    section = rules.split("## Topic Ownership", 1)[1].split("\n## ", 1)[0]
    owners = re.findall(r"`(backend/docs/[A-Za-z0-9_]+\.md)`", section)
    assert owners, "Topic Ownership table lists no documents"
    assert [owner for owner in owners if not (ROOT / owner).is_file()] == []


def test_skill_stays_a_navigation_entrypoint():
    assert SKILL.stat().st_size <= SKILL_MAX_BYTES
