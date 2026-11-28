from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_gateway_user_creation_grants_no_target_access():
    """New users start with no target grant; admins grant targets explicitly."""
    admin_http = (ROOT / "agent" / "internal" / "server" / "admin_http.go").read_text(
        encoding="utf-8"
    )
    create = admin_http.split("func (h *GatewayHub) HandleAdminUsers", 1)[1]
    create = create.split("\nfunc ", 1)[0]

    assert "CreateUserWithPassword" in create
    assert "GrantUser" not in create
    assert "SetUserAdmin" in create
