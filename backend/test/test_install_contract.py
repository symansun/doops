import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
INSTALL = ROOT / "scripts" / "install.sh"
MIGRATE_AUTH = ROOT / "scripts" / "migrate-gateway-auth.py"
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
    assert 'skills/doops-cli/bin' not in text
    assert '-contract' not in text
    assert "--target-token is no longer supported" in text
    assert 'doops login --target ${TARGET_NAME:-<target-name>}' in text
    assert text.index("--target-token is no longer supported") < text.index('echo "[1/4] Installing CLI')
    assert "Refusing to replace the CLI while config.json still contains per-target token fields." in text


def test_cli_release_builds_to_ignored_distribution_directory():
    build = BUILD.read_text()
    release = RELEASE_BUILD.read_text()

    assert 'expected_builder="linux/amd64 go1.24.13"' in build
    assert "run scripts/build-cli-release.sh instead" in build
    assert f'BUILDER_IMAGE="{BUILDER_IMAGE}"' in release
    assert "--platform linux/amd64" in release
    assert 'RELEASE_DIR="${DOOPS_CLI_RELEASE_DIR:-${REPO_ROOT}/dist/cli}"' in build
    assert 'RELEASE_DIR="${DOOPS_CLI_RELEASE_DIR:-${REPO_ROOT}/dist/cli}"' in release


def test_installer_rejects_legacy_target_token_before_replacing_cli():
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
                "--target-token",
                "legacy-token",
            ],
            env={**os.environ, "HOME": str(home)},
            text=True,
            capture_output=True,
            check=False,
        )

        assert result.returncode != 0
        assert "--target-token is no longer supported" in result.stderr
        assert cli.read_text() == "existing-cli"


def test_gateway_auth_migration_uses_one_selected_target_token():
    with tempfile.TemporaryDirectory() as raw_home:
        root = Path(raw_home)
        config = root / "config.json"
        config.write_text(
            json.dumps(
                {
                    "servers": [
                        {
                            "name": "first",
                            "gateway": "wss://Gateway.Example.com:443/",
                            "cluster": "a",
                            "instance": "one",
                            "token": "selected-token",
                        },
                        {
                            "name": "second",
                            "gateway": "https://gateway.example.com",
                            "cluster": "b",
                            "instance": "two",
                            "token": "stale-token",
                        },
                    ]
                }
            )
        )

        subprocess.run(
            [sys.executable, str(MIGRATE_AUTH), "--target", "first", "--config", str(config)],
            check=True,
            text=True,
            capture_output=True,
        )

        migrated = json.loads(config.read_text())
        assert all("token" not in server for server in migrated["servers"])
        auth = json.loads(config.with_name("auth.json").read_text())
        assert auth == {"tokens": {"https://gateway.example.com": "selected-token"}}


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
    assert "https://api.cnb.cool" in publish
    assert "asset-upload-url" in publish
    assert 'CNB_TOKEN is required' in publish
    assert "Agent image: ${image}" in publish
    assert "https://cnb.cool/${RELEASE_REPO}/-/releases/download/${RELEASE_TAG}" in verify
