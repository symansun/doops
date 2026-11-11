from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DEPLOY = ROOT / "scripts" / "deploy-gateway.sh"
FETCH = ROOT / "scripts" / "fetch-gateway-release.sh"


def test_gateway_deploy_detects_systemd_unit_without_pipefail_pipeline():
    text = DEPLOY.read_text()

    assert "systemctl list-unit-files" not in text
    assert "sudo -n systemctl cat doops-gateway.service >/dev/null" in text
    assert "REMOTE_LOCK=\"/run/lock/doops-gateway-deploy.lock\"" in text
    assert "REMOTE_TMP=\"/tmp/doops-gateway-deploy-${DEPLOY_ID}\"" in text
    assert "ControlMaster=auto" in text
    assert "ControlPersist=30" in text
    assert "ControlPath=${SSH_CONTROL_PATH}" in text
    assert "wait_for_local_gateway()" in text
    assert "wait_for_local_gateway 30" in text


def test_gateway_deploy_bootstraps_external_key_only_without_existing_ciphertext():
    text = DEPLOY.read_text()

    assert "password_ciphertext" in text
    assert "payload_ciphertext" in text
    assert "secrets.token_hex(32)" in text
    assert "gateway encrypted data exists but no legacy secret key is available" in text


def test_gateway_deploy_can_verify_with_configured_doops_target_without_exporting_token():
    text = DEPLOY.read_text()

    assert "--verify-target" in text
    assert "--verify-session" in text
    assert 'doops -session "${VERIFY_SESSION}" targets --target "${VERIFY_TARGET}"' in text
    assert "--verify-token-file" not in text
    assert "VERIFY_TOKEN" not in text


def test_gateway_release_fetches_the_ci_binary_from_release_assets():
    text = FETCH.read_text()

    assert "--release-tag" in text
    assert "--source-commit" in text
    assert "^[0-9a-f]{40}$" in text
    assert "doops-gateway-linux-amd64" in text
    assert "checksums.txt" in text
    assert "checksum mismatch" in text
    assert 'for marker in "${RELEASE_TAG}" "${SOURCE_COMMIT}"' in text

    build = (ROOT / "scripts" / "build-cli.sh").read_text()
    assert "doops-gateway-linux-amd64" in build

    dockerfile = (ROOT / "Dockerfile").read_text()
    assert "COPY --from=builder /app/doops-gateway" not in dockerfile
    assert "! test -e /app/doops-gateway" in dockerfile


def test_gateway_deploy_rejects_binary_without_exact_source_commit():
    text = DEPLOY.read_text()

    assert "--source-commit SHA" in text
    assert "a real Gateway deployment requires --source-commit" in text
    assert 'strings -a "${LOCAL_BIN}"' in text
    assert 'grep -Fxx "${SOURCE_COMMIT}" >/dev/null' in text
    assert "Gateway binary does not contain source commit" in text
