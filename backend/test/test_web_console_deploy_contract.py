from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DEPLOY = ROOT / "scripts" / "deploy-web-console.sh"
PROXY_DEPLOY = ROOT / "scripts" / "deploy-gateway-tls-proxy.sh"
NGINX = ROOT / "gateway" / "nginx" / "doops-proxy.conf"


def test_web_console_host_deploy_is_immutable_and_rollback_capable():
    deploy = DEPLOY.read_text()
    proxy_deploy = PROXY_DEPLOY.read_text()
    nginx = NGINX.read_text()

    assert '--image must use an immutable sha256 registry digest' in deploy
    assert 'DOOPS_CONSOLE_ALLOWED_GATEWAYS=$gateway' in deploy
    assert "127.0.0.1:3001:3000" in deploy
    assert "127.0.0.1:3000:3000" in deploy
    assert "doops-web-rollback-" in deploy
    assert 'docker rename "$backup" doops-web' in deploy
    assert 'docker inspect --format \'{{.Config.Image}}\' doops-web' in deploy

    assert "location ^~ /v1/" in nginx
    assert "location = /health" in nginx
    assert "location / {" in nginx
    assert nginx.count("proxy_pass http://127.0.0.1:42222;") == 2
    assert "proxy_pass http://127.0.0.1:3000;" in nginx

    assert "https://doops.l8ai.cn/v1/targets" in proxy_deploy
    assert "https://doops.l8ai.cn/" in proxy_deploy
    assert 'test "$web_code" = "200"' in proxy_deploy
    assert 'SSH=(ssh -p "$SSH_PORT" "${SSH_USER}@${HOST}")' in proxy_deploy
    assert 'SCP=(scp -P "$SSH_PORT")' in proxy_deploy
    assert "SSH_PREFIX" not in proxy_deploy
