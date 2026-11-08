import os
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DEV_LINK = ROOT / "scripts" / "dev-link.sh"
BUILD = ROOT / "scripts" / "build-cli.sh"


def test_dev_link_builds_the_repository_and_points_the_installed_cli_at_it():
    text = DEV_LINK.read_text()

    assert 'DOOPS_CLI_OUT="${BUILD_OUT}" bash "${REPO_ROOT}/backend/scripts/build-cli.sh"' in text
    assert 'ln -sfn "${BUILD_OUT}" "${CLI_OUT}"' in text
    assert 'BUILD_OUT="${REPO_ROOT}/bin/doops"' in text
    assert 'CLI_OUT="${HOME}/.local/bin/doops"' in text


def test_dev_link_keeps_the_release_install_as_the_only_supported_entrypoint():
    text = DEV_LINK.read_text()

    # The development link must never become a second install path: the
    # supported user entrypoint stays ~/.local/bin/doops installed by
    # install.sh from GitHub Release assets.
    assert 'backend/scripts/install.sh' in text
    # A real install product is preserved, never deleted.
    assert 'mv "${CLI_OUT}" "${CLI_OUT}.release"' in text
    assert 'rm -f "${CLI_OUT}"' not in text
    assert 'rm -rf "${CLI_OUT}"' not in text


def test_dev_link_links_the_project_skill_to_the_repository_sources():
    text = DEV_LINK.read_text()

    assert 'ln -sfn "${REPO_ROOT}/backend/skills/SKILL.md" "${SKILL_DIR}/SKILL.md"' in text
    assert 'ln -sfn "${REPO_ROOT}/backend/docs" "${SKILL_DIR}/docs"' in text
    # An existing installed Skill is backed up, not discarded.
    assert 'mv "${SKILL_DIR}/SKILL.md" "${SKILL_DIR}/SKILL.md.release"' in text
    assert 'mv "${SKILL_DIR}/docs" "${SKILL_DIR}/docs.release"' in text


def test_dev_link_does_not_publish_or_touch_a_target():
    text = DEV_LINK.read_text()

    for forbidden in (
        "gh workflow run",
        "git tag",
        "git push",
        "helm upgrade",
        "doops upgrade",
        "--apply",
    ):
        assert forbidden not in text, f"dev-link must not contain {forbidden}"


def test_dev_link_unlink_restores_the_release_entrypoint():
    text = DEV_LINK.read_text()

    assert "--unlink" in text
    assert 'unlink_path "${CLI_OUT}"' in text
    assert "正式版本请用 backend/scripts/install.sh 安装" in text


def test_dev_link_reports_the_built_commit():
    text = DEV_LINK.read_text()

    assert '"${BUILD_OUT}" version' in text
    assert 'commit=' in text


def test_dev_link_warns_that_a_dev_cli_cannot_upgrade_an_agent():
    # The upgrade path verifies the Agent's release_commit against the CLI's
    # own commit, so a development CLI (release=dev, commit=HEAD) would fail
    # the check and trigger an automatic rollback. The script must say so.
    text = DEV_LINK.read_text()

    assert "release_commit" in text
    assert "Agent 升级命令" in text
    assert "同 commit 的正式 CLI" in text


def test_dev_link_script_is_executable_and_valid_bash():
    assert os.access(DEV_LINK, os.X_OK), "dev-link.sh must be executable"

    result = subprocess.run(
        ["bash", "-n", str(DEV_LINK)], capture_output=True, text=True
    )
    assert result.returncode == 0, result.stderr

    result = subprocess.run(
        ["bash", str(DEV_LINK), "--help"], capture_output=True, text=True
    )
    assert result.returncode == 0, result.stderr
    assert "--build-only" in result.stdout
    assert "--unlink" in result.stdout


def test_build_cli_script_writes_the_repository_bin_path_the_dev_link_uses():
    text = BUILD.read_text()

    assert 'DEFAULT_OUT="${DOOPS_CLI_OUT:-${REPO_ROOT}/bin/doops}"' in text
    assert 'DOOPS_CLI_OUT' in text
