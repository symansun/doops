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
    assert '"${candidate}" version' in text
    assert "doops.sh/cli-contract/v3" in text
    assert "${CLI_OUT} version" in text
    assert "\n  doops list" not in text
    assert 'skills/doops-cli/bin' not in text
    assert " --contract" not in text
    assert '${CLI_OUT} login --target ${TARGET_NAME:-<target-name>}' in text
    assert 'DOOPS_CONFIG="${CANONICAL_CONFIG_FILE}" "${candidate}" list' in text
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

        platform = subprocess.run(
            ["bash", "-c", "uname -s | tr '[:upper:]' '[:lower:]'; uname -m"],
            check=True,
            text=True,
            capture_output=True,
        ).stdout.splitlines()
        arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[
            platform[1]
        ]
        artifact_name = f"doops-{platform[0]}-{arch}"
        artifact = fixtures / artifact_name
        release_commit = "a" * 40
        artifact.write_text(
            "#!/usr/bin/env bash\n"
            "if [[ \"${1:-}\" == version ]]; then\n"
            f"  printf 'version=v-test commit={release_commit} contract=doops.sh/cli-contract/v3\\n'\n"
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
        assert "version=v-test" in receipt
        assert f"artifact={artifact_name}" in receipt
        assert f"sha256={checksum}" in receipt
        assert f"commit={release_commit}" in receipt
        assert "contract=doops.sh/cli-contract/v3" in receipt
        assert f"path={installed}" in receipt
        assert not (home / ".agent" / "skills" / "doops" / "doops-cli" / "bin").exists()

        installed.write_text("keep-existing-cli")
        artifact.write_text(
            "#!/usr/bin/env bash\n"
            "printf 'version=dev commit=unknown contract=doops.sh/cli-contract/v3\\n'\n"
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


def prepare_fake_cli_release(root: Path, version: str):
    fixtures = root / "fixtures"
    fake_bin = root / "bin"
    fixtures.mkdir(exist_ok=True)
    fake_bin.mkdir(exist_ok=True)
    platform = subprocess.run(
        ["bash", "-c", "uname -s | tr '[:upper:]' '[:lower:]'; uname -m"],
        check=True,
        text=True,
        capture_output=True,
    ).stdout.splitlines()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[
        platform[1]
    ]
    artifact_name = f"doops-{platform[0]}-{arch}"
    artifact = fixtures / artifact_name
    release_commit = "a" * 40
    artifact.write_text(
        "#!/usr/bin/env bash\n"
        "set -euo pipefail\n"
        "case \"${1:-}\" in\n"
        "  version)\n"
        f"    printf 'version={version} commit={release_commit} contract=doops.sh/cli-contract/v3\\n'\n"
        "    ;;\n"
        "  list)\n"
        "    if grep -q '\"token\"' \"${DOOPS_CONFIG}\"; then\n"
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
