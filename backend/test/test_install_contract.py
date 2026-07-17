from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
INSTALL = ROOT / "scripts" / "install.sh"
BUILD = ROOT / "scripts" / "build-cli.sh"
RELEASE_BUILD = ROOT / "scripts" / "build-cli-release.sh"
CI = ROOT.parent / ".cnb.yml"
BUILDER_IMAGE = (
    "docker.io/library/golang:1.24-alpine@"
    "sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191"
)


def test_cli_install_replaces_existing_binary_atomically():
    text = INSTALL.read_text()

    assert 'candidate="$(mktemp "$(dirname "${CLI_OUT}")/.doops-install.XXXXXX")"' in text
    assert 'install -m 0755 "${prebuilt}" "${candidate}"' in text
    assert 'contract="$("${candidate}" -contract 2>/dev/null || true)"' in text
    assert '[[ "${contract}" != "doops.sh/cicd-contract/v3" ]]' in text
    assert 'mv -f "${candidate}" "${CLI_OUT}"' in text
    assert 'cp "${prebuilt}" "${CLI_OUT}"' not in text


def test_cli_release_uses_one_pinned_linux_builder():
    build = BUILD.read_text()
    release = RELEASE_BUILD.read_text()
    ci = CI.read_text()

    assert 'expected_builder="linux/amd64 go1.24.13"' in build
    assert "run scripts/build-cli-release.sh instead" in build
    assert f'BUILDER_IMAGE="{BUILDER_IMAGE}"' in release
    assert "--platform linux/amd64" in release
    assert ci.count(f"image: {BUILDER_IMAGE}") == 4
