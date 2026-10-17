from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_gateway_still_creates_a_wildcard_bootstrap_grant():
    """Cross-language contract for the source behavior documented above."""
    admin_http = (ROOT / "agent" / "internal" / "server" / "admin_http.go").read_text(
        encoding="utf-8"
    )

    assert 'ScopeGrant{Cluster: "*", Instance: "*"}' in admin_http
