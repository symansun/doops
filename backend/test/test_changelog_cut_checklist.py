import os
import subprocess
import sys
from pathlib import Path

import pytest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "changelog-cut-checklist.py"

CHANGELOG_TEMPLATE = """# Changelog

## [0.3.39] — 2026-11-25

### Added

- entry one

### Fixed

- entry two

## [0.3.38] — 2026-11-24

- old entry
"""


def git(repo, *args):
    return subprocess.run(
        ["git", *args],
        cwd=repo,
        capture_output=True,
        text=True,
        env={
            **os.environ,
            "GIT_AUTHOR_NAME": "qa",
            "GIT_AUTHOR_EMAIL": "qa@example.invalid",
            "GIT_COMMITTER_NAME": "qa",
            "GIT_COMMITTER_EMAIL": "qa@example.invalid",
            "GIT_CONFIG_NOSYSTEM": "1",
        },
    )


def write(repo, relative, text):
    path = repo / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def commit(repo, message, files):
    for relative, text in files.items():
        write(repo, relative, text)
        assert git(repo, "add", relative).returncode == 0
    result = git(repo, "commit", "-q", "-m", message)
    assert result.returncode == 0, result.stderr
    return git(repo, "rev-parse", "HEAD").stdout.strip()


def run(repo, *args):
    return subprocess.run(
        [sys.executable, str(SCRIPT), *args],
        cwd=repo,
        capture_output=True,
        text=True,
    )


def make_repo(tmp_path, changelog=CHANGELOG_TEMPLATE):
    repo = tmp_path / "repo"
    repo.mkdir()
    assert git(repo, "init", "-q", "-b", "main").returncode == 0
    assert git(repo, "config", "user.name", "qa").returncode == 0
    assert git(repo, "config", "user.email", "qa@example.invalid").returncode == 0
    commit(repo, "chore: bootstrap", {"CHANGELOG.md": changelog})
    assert git(repo, "tag", "v0.3.38").returncode == 0
    return repo


def test_two_real_tags_lists_feat_and_fix(tmp_path):
    repo = make_repo(tmp_path)
    feat = commit(repo, "feat(cli): add --json flag", {"a.txt": "a\n", "b.txt": "b\n"})
    fix = commit(repo, "fix(agent)!: stop dropping pty output", {"c.txt": "c\n"})
    commit(repo, "docs: tweak readme", {"readme.md": "r\n"})
    assert git(repo, "tag", "v0.3.39").returncode == 0

    result = run(repo, "v0.3.38", "v0.3.39")

    assert result.returncode == 0, result.stderr
    assert f"[ ] {feat[:8]}  feat" in result.stdout
    assert f"[ ] {fix[:8]}  fix!" in result.stdout
    assert "2 files" in result.stdout
    assert "1 files" in result.stdout
    assert "docs:" not in result.stdout
    assert "CHANGELOG: section [0.3.39] at line 3, entries: 2" in result.stdout


def test_docs_and_chore_are_filtered_out(tmp_path):
    repo = make_repo(tmp_path)
    commit(repo, "feat(api): add endpoint", {"a.txt": "a\n"})
    commit(repo, "fix(api): correct status code", {"b.txt": "b\n"})
    commit(repo, "docs: explain endpoint", {"readme.md": "r\n"})
    commit(repo, "chore: bump deps", {"package.json": "{}\n"})
    assert git(repo, "tag", "v0.3.39").returncode == 0

    result = run(repo, "v0.3.38", "v0.3.39")

    assert result.returncode == 0, result.stderr
    checklist = [line for line in result.stdout.splitlines() if line.startswith("[ ] ")]
    assert len(checklist) == 2
    assert "feat/api" not in result.stdout
    assert "docs: explain endpoint" not in result.stdout
    assert "chore: bump deps" not in result.stdout
    assert "非 feat/fix 提交（已忽略）: 2 条" in result.stdout


def test_missing_candidate_tag_falls_back_to_head(tmp_path):
    repo = make_repo(tmp_path)
    feat = commit(repo, "feat(web): show empty state", {"a.txt": "a\n"})

    result = run(repo, "v0.3.38", "v0.4.0")

    assert result.returncode == 0, result.stderr
    assert "candidate tag does not exist, using HEAD" in result.stdout
    assert f"[ ] {feat[:8]}  feat" in result.stdout
    assert "CHANGELOG: section" not in result.stdout


def test_missing_previous_tag_exits_2(tmp_path):
    repo = make_repo(tmp_path)
    assert git(repo, "tag", "v0.3.39").returncode == 0

    result = run(repo, "v0.0.1", "v0.3.39")

    assert result.returncode == 2
    assert "previous tag 'v0.0.1' does not exist" in result.stderr


def test_changelog_section_missing_exits_2(tmp_path):
    repo = make_repo(tmp_path, changelog="# Changelog\n\n## [0.3.38] — 2026-11-24\n\n- old\n")
    commit(repo, "feat(cli): change something", {"a.txt": "a\n"})
    assert git(repo, "tag", "v0.3.39").returncode == 0

    result = run(repo, "v0.3.38", "v0.3.39")

    assert result.returncode == 2
    assert "no `## [0.3.39]` section" in result.stderr


def test_unchecked_commits_are_not_a_gate(tmp_path):
    repo = make_repo(tmp_path, changelog="# Changelog\n\n## [0.3.39] — 2026-11-25\n\n## [0.3.38] — 2026-11-24\n\n- old\n")
    commit(repo, "feat(cli): shipped without a changelog entry", {"a.txt": "a\n"})
    assert git(repo, "tag", "v0.3.39").returncode == 0

    result = run(repo, "v0.3.38", "v0.3.39")

    assert result.returncode == 0, result.stderr
    assert "entries: 0" in result.stdout
    assert "1 条" in result.stdout


def test_missing_arguments_exit_2(tmp_path):
    repo = make_repo(tmp_path)

    result = run(repo)

    assert result.returncode == 2
    assert "expected exactly two tags" in result.stderr
