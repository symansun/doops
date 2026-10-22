from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "build-web-image.sh"
CI = ROOT.parent / ".cnb.yml"


def test_web_image_release_builds_smokes_and_pushes_immutable_image():
    script = SCRIPT.read_text()
    ci = CI.read_text()

    assert 'WEB_IMAGE="${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}/web:${DOOPS_RELEASE_TAG}"' in script
    assert '-f "${REPO_ROOT}/../Dockerfile.web"' in script
    assert '--label org.opencontainers.image.revision="${CNB_COMMIT}"' in script
    assert '--label org.opencontainers.image.version="${DOOPS_RELEASE_TAG}"' in script
    assert "DOOPS_CONSOLE_ALLOWED_GATEWAYS=https://doops.invalid" in script
    assert 'fetch("http://127.0.0.1:3000")' in script
    assert 'docker push "${WEB_IMAGE}"' in script
    assert '[[ "${DOOPS_PUBLISH_LATEST:-}" == "1" && "${DOOPS_RELEASE_TAG}" != "latest" ]]' in script
    assert 'latest_image="${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}/web:latest"' in script
    assert 'docker push "${latest_image}"' in script

    invocation = 'DOOPS_PUBLISH_LATEST=1 DOOPS_RELEASE_TAG="${CNB_COMMIT}" bash backend/scripts/build-web-image.sh'
    assert invocation in ci
    tag_invocation = 'DOOPS_RELEASE_TAG="${CNB_BRANCH}" bash backend/scripts/build-web-image.sh'
    assert tag_invocation in ci
    assert "DOOPS_PUBLISH_LATEST" not in ci.split("tag_push:", 1)[1]
