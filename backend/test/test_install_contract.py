import json
import hashlib
import os
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
INSTALL = ROOT / "scripts" / "install.sh"
BUILD = ROOT / "scripts" / "build-cli.sh"
RELEASE_BUILD = ROOT / "scripts" / "build-cli-release.sh"
PUBLISH = ROOT / "scripts" / "publish-cli-release.mjs"
VERIFY_PUBLISHED = ROOT / "scripts" / "verify-published-cli-release.sh"
CI = ROOT.parent / ".cnb.yml"
BUILDER_IMAGE = (
    "docker.io/library/golang:1.24-alpine@"
    "sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191"
)


def test_cli_install_downloads_named_release_asset_and_replaces_binary_atomically():
    text = INSTALL.read_text()

    assert '--version is required' in text
    assert 'https://cnb.cool/l8ai/ai/doops.sh/-/releases/download/${RELEASE_TAG}' in text
    assert 'curl --fail --location --silent --show-error' in text
    assert 'install -m 0755 "${candidate}" "${replacement}"' in text
    assert 'mv -f "${replacement}" "${CLI_OUT}"' in text
    assert 'receipt="${receipt_dir}/install.txt"' in text
    assert "source=cnb-release-assets" in text
    assert "skill_commit" in text
    assert '"${candidate}" version' in text
    assert "doops.sh/cli-contract/v3" in text
    assert "${CLI_OUT} version" in text
    assert "\n  doops list" not in text
    assert 'skills/doops-cli/bin' not in text
    assert 'backend/cli/bin' not in text
    assert '"${candidate}" --contract' in text
    assert '${CLI_OUT} -session setup login --target ${TARGET_NAME:-<target-name>}' in text
    assert 'DOOPS_CONFIG= "${candidate}" list' in text
    assert "python3 - " not in text
    assert text.index('Existing doops config rejected by release CLI') < text.index(
        'install -m 0755 "${candidate}" "${replacement}"'
    )


def test_cli_release_builds_to_ignored_distribution_directory():
    build = BUILD.read_text()
    release = RELEASE_BUILD.read_text()

    assert 'expected_builder="linux/amd64 go1.24.13"' in build
    assert "run scripts/build-cli-release.sh instead" in build
    assert f'BUILDER_IMAGE="{BUILDER_IMAGE}"' in release
    assert "--platform linux/amd64" in release
    assert 'RELEASE_DIR="${DOOPS_CLI_RELEASE_DIR:-${REPO_ROOT}/dist/cli}"' in build
    assert 'RELEASE_DIR="${DOOPS_CLI_RELEASE_DIR:-${REPO_ROOT}/dist/cli}"' in release
    assert '-X main.cliBuildVersion=${CLI_VERSION}' in build
    assert '-X main.cliBuildCommit=${CLI_COMMIT}' in build


def test_installer_rejects_unsupported_target_fields_before_replacing_cli():
    with tempfile.TemporaryDirectory() as raw_home:
        home = Path(raw_home)
        fake_bin, release_env = prepare_fake_cli_release(Path(raw_home), "v-test")
        cli = home / ".local" / "bin" / "doops"
        cli.parent.mkdir(parents=True)
        cli.write_text("existing-cli")
        config = home / ".agent" / "skills" / "doops" / "config.json"
        config.parent.mkdir(parents=True)
        config.write_text(
            json.dumps(
                {
                    "servers": [
                        {
                            "name": "legacy",
                            "gateway": "https://gateway.example.com",
                            "cluster": "prod",
                            "instance": "node",
                            "token": "retired",
                        }
                    ]
                }
            )
        )

        result = subprocess.run(
            [
                "bash",
                str(INSTALL),
                "--version",
                "v-test",
                "--project",
                str(ROOT.parent),
            ],
            env={
                **os.environ,
                **release_env,
                "HOME": str(home),
                "PATH": f"{fake_bin}:{os.environ['PATH']}",
            },
            text=True,
            capture_output=True,
            check=False,
        )

        assert result.returncode != 0
        assert 'unknown field "token"' in result.stderr
        assert cli.read_text() == "existing-cli"


def test_installer_requires_complete_gateway_route_before_replacing_cli():
    with tempfile.TemporaryDirectory() as raw_home:
        home = Path(raw_home)
        cli = home / ".local" / "bin" / "doops"
        cli.parent.mkdir(parents=True)
        cli.write_text("existing-cli")

        result = subprocess.run(
            [
                "bash",
                str(INSTALL),
                "--version",
                "unused",
                "--project",
                str(ROOT.parent),
                "--target-name",
                "incomplete",
                "--target-gateway",
                "https://gateway.example.com",
            ],
            env={**os.environ, "HOME": str(home)},
            text=True,
            capture_output=True,
            check=False,
        )

        assert result.returncode == 2
        assert "requires --target-name, --target-gateway, --target-cluster, and --target-instance" in result.stderr
        assert cli.read_text() == "existing-cli"


def test_installer_rejects_legacy_only_config_before_replacing_any_artifact(tmp_path):
    home = tmp_path / "home"
    project = tmp_path / "project"
    project.mkdir()
    old_cli = home / ".local/bin/doops"
    old_cli.parent.mkdir(parents=True)
    old_cli.write_text("existing-cli")
    old_skill = project / ".agent/skills/doops/SKILL.md"
    old_skill.parent.mkdir(parents=True)
    old_skill.write_text("existing-skill")
    legacy = home / ".config/doops/config.json"
    legacy.parent.mkdir(parents=True)
    legacy.write_text('{"servers":[{"name":"legacy","ip":"192.0.2.1"}]}')
    fake_bin, release_env = prepare_fake_cli_release(tmp_path, "v-test")
    result = run_installer(
        ROOT.parent, home, project, fake_bin,
        {**release_env, "DOOPS_CONFIG": str(tmp_path / "isolated/config.json")},
    )
    assert result.returncode != 0
    assert "retired" in result.stderr
    assert old_cli.read_text() == "existing-cli"
    assert old_skill.read_text() == "existing-skill"
    assert not (home / ".local/share/doops/install.txt").exists()
    assert not (home / ".agent/skills/doops/config.json").exists()


def test_installer_places_release_asset_only_in_standard_cli_path():
    with tempfile.TemporaryDirectory() as raw_root:
        root = Path(raw_root)
        home = root / "home"
        fixtures = root / "fixtures"
        fake_bin = root / "bin"
        project = root / "project"
        home.mkdir()
        fixtures.mkdir()
        fake_bin.mkdir()
        project.mkdir()

        platform, arch = host_platform()
        artifact_name = f"doops-{platform[0]}-{arch}"
        artifact = fixtures / artifact_name
        release_commit = git_commit(ROOT.parent)
        artifact.write_text(
            "#!/usr/bin/env bash\n"
            "if [[ \"${1:-}\" == version ]]; then\n"
            f"  printf 'release=v-test commit={release_commit}\\n'\n"
            "elif [[ \"${1:-}\" == --contract ]]; then\n"
            "  printf 'doops.sh/cli-contract/v3\\n'\n"
            "fi\n"
        )
        artifact.chmod(0o755)
        checksum = hashlib.sha256(artifact.read_bytes()).hexdigest()
        (fixtures / "checksums.txt").write_text(f"{checksum}  {artifact_name}\n")

        curl = fake_bin / "curl"
        curl.write_text(
            """#!/usr/bin/env bash
set -euo pipefail
out=""
url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) out="$2"; shift 2 ;;
    --*) shift ;;
    *) url="$1"; shift ;;
  esac
done
cp "${DOOPS_TEST_RELEASE_FIXTURES}/${url##*/}" "${out}"
"""
        )
        curl.chmod(0o755)

        result = subprocess.run(
            [
                "bash",
                str(INSTALL),
                "--version",
                "v-test",
                "--project",
                str(project),
            ],
            env={
                **os.environ,
                "HOME": str(home),
                "PATH": f"{fake_bin}:{os.environ['PATH']}",
                "DOOPS_TEST_RELEASE_FIXTURES": str(fixtures),
            },
            text=True,
            capture_output=True,
            check=False,
        )

        assert result.returncode == 0, result.stderr
        installed = home / ".local" / "bin" / "doops"
        assert installed.read_bytes() == artifact.read_bytes()
        receipt = (home / ".local" / "share" / "doops" / "install.txt").read_text()
        assert "release=v-test" in receipt
        assert f"artifact={artifact_name}" in receipt
        assert f"sha256={checksum}" in receipt
        assert f"commit={release_commit}" in receipt
        assert "contract=doops.sh/cli-contract/v3" in receipt
        assert f"path={installed}" in receipt
        assert not (home / ".agent" / "skills" / "doops" / "doops-cli" / "bin").exists()

        installed.write_text("keep-existing-cli")
        artifact.write_text(
            "#!/usr/bin/env bash\n"
            "printf 'release=dev commit=unknown\\n'\n"
        )
        artifact.chmod(0o755)
        bad_checksum = hashlib.sha256(artifact.read_bytes()).hexdigest()
        (fixtures / "checksums.txt").write_text(f"{bad_checksum}  {artifact_name}\n")
        rejected = subprocess.run(
            [
                "bash",
                str(INSTALL),
                "--version",
                "v-test",
                "--project",
                str(project),
            ],
            env={
                **os.environ,
                "HOME": str(home),
                "PATH": f"{fake_bin}:{os.environ['PATH']}",
                "DOOPS_TEST_RELEASE_FIXTURES": str(fixtures),
            },
            text=True,
            capture_output=True,
            check=False,
        )
        assert rejected.returncode != 0
        assert "identity mismatch" in rejected.stderr
        assert installed.read_text() == "keep-existing-cli"


def test_installer_stages_skill_and_docs_from_the_release_commit():
    with tempfile.TemporaryDirectory() as raw_root:
        root = Path(raw_root)
        repo, release_commit = prepare_synthetic_repo(
            root / "source",
            skill="release skill\n",
            docs={"release.md": "release docs\n"},
        )
        home = root / "home"
        project = root / "project"
        home.mkdir()
        project.mkdir()
        fake_bin, release_env = prepare_fake_cli_release(
            root / "release",
            "v-test",
            commit=release_commit,
        )

        result = run_installer(
            repo,
            home,
            project,
            fake_bin,
            release_env,
        )

        assert result.returncode == 0, result.stderr
        installed_skill = project / ".agent" / "skills" / "doops" / "SKILL.md"
        installed_doc = project / ".agent" / "skills" / "doops" / "docs" / "release.md"
        assert installed_skill.read_text() == "release skill\n"
        assert installed_doc.read_text() == "release docs\n"
        receipt = (home / ".local" / "share" / "doops" / "install.txt").read_text()
        assert f"skill_commit={release_commit}" in receipt


def test_installer_uses_release_commit_when_worktree_skill_and_docs_are_updated():
    with tempfile.TemporaryDirectory() as raw_root:
        root = Path(raw_root)
        repo, release_commit = prepare_synthetic_repo(
            root / "source",
            skill="release skill\n",
            docs={"release.md": "release docs\n"},
        )
        (repo / "backend" / "skills" / "SKILL.md").write_text("working tree skill\n")
        (repo / "backend" / "docs" / "release.md").write_text("working tree docs\n")
        home = root / "home"
        project = root / "project"
        home.mkdir()
        project.mkdir()
        fake_bin, release_env = prepare_fake_cli_release(
            root / "release",
            "v-test",
            commit=release_commit,
        )

        result = run_installer(
            repo,
            home,
            project,
            fake_bin,
            release_env,
        )

        assert result.returncode == 0, result.stderr
        installed_skill = project / ".agent" / "skills" / "doops" / "SKILL.md"
        installed_doc = project / ".agent" / "skills" / "doops" / "docs" / "release.md"
        assert installed_skill.read_text() == "release skill\n"
        assert installed_doc.read_text() == "release docs\n"


def test_installer_preserves_existing_installation_when_release_commit_is_missing():
    with tempfile.TemporaryDirectory() as raw_root:
        root = Path(raw_root)
        repo, _ = prepare_synthetic_repo(
            root / "source",
            skill="release skill\n",
            docs={"release.md": "release docs\n"},
        )
        home = root / "home"
        project = root / "project"
        home.mkdir()
        project.mkdir()
        old_cli = home / ".local" / "bin" / "doops"
        old_cli.parent.mkdir(parents=True)
        old_cli.write_text("old cli\n")
        skill_dest = project / ".agent" / "skills" / "doops"
        (skill_dest / "docs").mkdir(parents=True)
        (skill_dest / "SKILL.md").write_text("old skill\n")
        (skill_dest / "docs" / "old.md").write_text("old docs\n")
        config = home / ".agent" / "skills" / "doops" / "config.json"
        config.parent.mkdir(parents=True)
        config.write_text('{"servers":[]}\n')
        fake_bin, release_env = prepare_fake_cli_release(
            root / "release",
            "v-test",
            commit="b" * 40,
        )

        result = run_installer(
            repo,
            home,
            project,
            fake_bin,
            release_env,
        )

        assert result.returncode != 0
        assert "unavailable in local git objects" in result.stderr
        assert old_cli.read_text() == "old cli\n"
        assert (skill_dest / "SKILL.md").read_text() == "old skill\n"
        assert (skill_dest / "docs" / "old.md").read_text() == "old docs\n"
        assert config.read_text() == '{"servers":[]}\n'


def test_installer_preserves_existing_installation_when_release_skill_or_docs_are_missing():
    for missing in ("skill", "docs"):
        with tempfile.TemporaryDirectory() as raw_root:
            root = Path(raw_root)
            repo, release_commit = prepare_synthetic_repo(
                root / "source",
                skill=None if missing == "skill" else "release skill\n",
                docs={} if missing == "docs" else {"release.md": "release docs\n"},
            )
            home = root / "home"
            project = root / "project"
            home.mkdir()
            project.mkdir()
            old_cli = home / ".local" / "bin" / "doops"
            old_cli.parent.mkdir(parents=True)
            old_cli.write_text("old cli\n")
            skill_dest = project / ".agent" / "skills" / "doops"
            (skill_dest / "docs").mkdir(parents=True)
            (skill_dest / "SKILL.md").write_text("old skill\n")
            (skill_dest / "docs" / "old.md").write_text("old docs\n")
            config = home / ".agent" / "skills" / "doops" / "config.json"
            config.parent.mkdir(parents=True)
            config.write_text('{"servers":[]}\n')
            fake_bin, release_env = prepare_fake_cli_release(
                root / "release",
                "v-test",
                commit=release_commit,
            )

            result = run_installer(
                repo,
                home,
                project,
                fake_bin,
                release_env,
            )

            assert result.returncode != 0
            assert "missing" in result.stderr
            assert old_cli.read_text() == "old cli\n"
            assert (skill_dest / "SKILL.md").read_text() == "old skill\n"
            assert (skill_dest / "docs" / "old.md").read_text() == "old docs\n"
            assert config.read_text() == '{"servers":[]}\n'


def test_installer_validates_with_release_cli_when_path_python_is_broken():
    with tempfile.TemporaryDirectory() as raw_root:
        root = Path(raw_root)
        home = root / "home"
        project = root / "project"
        home.mkdir()
        project.mkdir()
        fake_bin, release_env = prepare_fake_cli_release(root, "v-test")
        marker = root / "python-invoked"
        python = fake_bin / "python3"
        python.write_text(
            "#!/usr/bin/env bash\n"
            f"touch {marker}\n"
            "exit 137\n"
        )
        python.chmod(0o755)
        config = home / ".agent" / "skills" / "doops" / "config.json"
        config.parent.mkdir(parents=True)
        config.write_text(
            json.dumps(
                {
                    "servers": [
                        {
                            "name": "gw",
                            "gateway": "https://gateway.example.com",
                            "cluster": "prod",
                            "instance": "node",
                        }
                    ]
                }
            )
        )

        result = subprocess.run(
            [
                "bash",
                str(INSTALL),
                "--version",
                "v-test",
                "--project",
                str(project),
            ],
            env={
                **os.environ,
                **release_env,
                "HOME": str(home),
                "PATH": f"{fake_bin}:{os.environ['PATH']}",
            },
            text=True,
            capture_output=True,
            check=False,
        )

        assert result.returncode == 0, result.stderr
        assert not marker.exists()
        assert (home / ".local" / "bin" / "doops").exists()


def test_tag_pipeline_publishes_and_verifies_release_assets():
    ci = CI.read_text()
    publish = PUBLISH.read_text()
    verify = VERIFY_PUBLISHED.read_text()

    assert "tag_push:" in ci
    assert 'DOOPS_RELEASE_TAG="${CNB_BRANCH}" bash backend/scripts/build-agent-image.sh' in ci
    assert "build release agent image" in ci
    assert "publish CLI release assets" in ci
    assert '--repo "${CNB_REPO_SLUG_LOWERCASE}"' in ci
    assert '--repo "${CNB_REPO}"' not in ci
    assert "verify published CLI release assets" in ci
    assert 'DOOPS_RELEASE_COMMIT="${CNB_COMMIT}"' in ci
    assert "https://api.cnb.cool" in publish
    assert "asset-upload-url" in publish
    assert 'CNB_TOKEN is required' in publish
    assert "Agent image: ${image}" in publish
    assert "https://cnb.cool/${RELEASE_REPO}/-/releases/download/${RELEASE_TAG}" in verify
    assert "Published CLI identity mismatch" in verify
    assert "Published CLI identity marker missing" in verify


def prepare_fake_cli_release(root: Path, version: str, commit: str | None = None):
    fixtures = root / "fixtures"
    fake_bin = root / "bin"
    fixtures.mkdir(parents=True, exist_ok=True)
    fake_bin.mkdir(parents=True, exist_ok=True)
    platform, arch = host_platform()
    artifact_name = f"doops-{platform[0]}-{arch}"
    artifact = fixtures / artifact_name
    release_commit = commit or git_commit(ROOT.parent)
    artifact.write_text(
        "#!/usr/bin/env bash\n"
        "set -euo pipefail\n"
        "case \"${1:-}\" in\n"
        "  version)\n"
        f"    printf 'release={version} commit={release_commit}\\n'\n"
        "    ;;\n"
        "  --contract)\n"
        "    printf 'doops.sh/cli-contract/v3\\n'\n"
        "    ;;\n"
        "  list)\n"
        "    config=\"${DOOPS_CONFIG:-${HOME}/.agent/skills/doops/config.json}\"\n"
        "    if [[ -z \"${DOOPS_CONFIG:-}\" && ! -f \"$config\" && -f \"${HOME}/.config/doops/config.json\" ]]; then\n"
        "      printf 'legacy config is retired\\n' >&2\n"
        "      exit 1\n"
        "    fi\n"
        "    if [[ -f \"$config\" ]] && grep -q '\"token\"' \"$config\"; then\n"
        "      printf 'Error loading config: json: unknown field \"token\"\\n' >&2\n"
        "      exit 1\n"
        "    fi\n"
        "    ;;\n"
        "esac\n"
    )
    artifact.chmod(0o755)
    checksum = hashlib.sha256(artifact.read_bytes()).hexdigest()
    (fixtures / "checksums.txt").write_text(f"{checksum}  {artifact_name}\n")
    curl = fake_bin / "curl"
    curl.write_text(
        """#!/usr/bin/env bash
set -euo pipefail
out=""
url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) out="$2"; shift 2 ;;
    --*) shift ;;
    *) url="$1"; shift ;;
  esac
done
cp "${DOOPS_TEST_RELEASE_FIXTURES}/${url##*/}" "${out}"
"""
    )
    curl.chmod(0o755)
    return fake_bin, {"DOOPS_TEST_RELEASE_FIXTURES": str(fixtures)}


def host_platform():
    platform = subprocess.run(
        ["bash", "-c", "uname -s | tr '[:upper:]' '[:lower:]'; uname -m"],
        check=True,
        text=True,
        capture_output=True,
    ).stdout.splitlines()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[
        platform[1]
    ]
    return platform, arch


def git_commit(repo: Path):
    return subprocess.run(
        ["git", "-C", str(repo), "rev-parse", "HEAD"],
        check=True,
        text=True,
        capture_output=True,
    ).stdout.strip()


def prepare_synthetic_repo(root: Path, skill: str | None, docs: dict[str, str]):
    script = root / "backend" / "scripts" / "install.sh"
    script.parent.mkdir(parents=True)
    script.write_bytes(INSTALL.read_bytes())
    script.chmod(0o755)
    if skill is not None:
        skill_path = root / "backend" / "skills" / "SKILL.md"
        skill_path.parent.mkdir(parents=True, exist_ok=True)
        skill_path.write_text(skill)
    for name, content in docs.items():
        doc_path = root / "backend" / "docs" / name
        doc_path.parent.mkdir(parents=True, exist_ok=True)
        doc_path.write_text(content)

    subprocess.run(["git", "-C", str(root), "init", "-q"], check=True)
    subprocess.run(
        ["git", "-C", str(root), "config", "user.email", "test@example.com"],
        check=True,
    )
    subprocess.run(
        ["git", "-C", str(root), "config", "user.name", "Install Test"],
        check=True,
    )
    subprocess.run(["git", "-C", str(root), "add", "backend"], check=True)
    subprocess.run(
        ["git", "-C", str(root), "commit", "-q", "-m", "release sources"],
        check=True,
    )
    return root, git_commit(root)


def run_installer(
    repo: Path,
    home: Path,
    project: Path,
    fake_bin: Path,
    release_env: dict[str, str],
):
    return subprocess.run(
        [
            "bash",
            str(repo / "backend" / "scripts" / "install.sh"),
            "--version",
            "v-test",
            "--project",
            str(project),
        ],
        env={
            **os.environ,
            **release_env,
            "HOME": str(home),
            "PATH": f"{fake_bin}:{os.environ['PATH']}",
        },
        text=True,
        capture_output=True,
        check=False,
    )
