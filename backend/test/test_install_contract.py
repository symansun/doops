from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
INSTALL = ROOT / "scripts" / "install.sh"


def test_cli_install_replaces_existing_binary_atomically():
    text = INSTALL.read_text()

    assert 'candidate="$(mktemp "$(dirname "${CLI_OUT}")/.doops-install.XXXXXX")"' in text
    assert 'install -m 0755 "${prebuilt}" "${candidate}"' in text
    assert 'mv -f "${candidate}" "${CLI_OUT}"' in text
    assert 'cp "${prebuilt}" "${CLI_OUT}"' not in text
