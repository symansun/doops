from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_skill_requires_wildcard_grant_removal_and_end_to_end_verification():
    skill = (ROOT / "skills" / "SKILL.md").read_text(encoding="utf-8")

    assert "POST /v1/admin/users" in skill
    assert "wildcard bootstrap grant" in skill
    assert "wildcard scope" in skill
    assert "删除所有" in skill
    assert "wildcard grant 数为 0" in skill
    assert "DOOPS_CONFIG" in skill
    assert "撤销验证登录 token" in skill
    assert "/v1/admin/users/disable" in skill


def test_user_management_docs_match_current_gateway_contract():
    usage = (ROOT / "docs" / "DOOPS_USAGE_GUIDE.md").read_text(encoding="utf-8")
    api = (ROOT / "docs" / "DOOPS_API_GUIDE.md").read_text(encoding="utf-8")
    tunnel = (ROOT / "docs" / "DOOPS_GATEWAY_TUNNEL.md").read_text(encoding="utf-8")

    for text in (usage, api, tunnel):
        assert "/v1/admin/users" in text
        assert "/v1/admin/grants" in text

    assert "actions" in usage
    assert "admin:false" in usage
    assert "doops admin token revoke" in usage
    assert "--workspace" in usage
    assert "默认拒绝" in api
    assert "bootstrap 行为" in tunnel


def test_gateway_still_creates_a_wildcard_bootstrap_grant():
    """Cross-language contract for the source behavior documented above."""
    admin_http = (ROOT / "agent" / "internal" / "server" / "admin_http.go").read_text(
        encoding="utf-8"
    )

    assert 'ScopeGrant{Cluster: "*", Instance: "*"}' in admin_http
