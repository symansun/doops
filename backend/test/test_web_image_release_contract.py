from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "build-web-image.sh"
CI = ROOT.parent / ".github/workflows/release.yml"


def test_web_image_release_builds_smokes_and_pushes_immutable_image():
    script = SCRIPT.read_text()
    ci = CI.read_text()

    assert 'WEB_IMAGE="${DOOPS_WEB_IMAGE_REPOSITORY}:${DOOPS_RELEASE_TAG}"' in script
    assert '-f "${REPO_ROOT}/../Dockerfile.web"' in script
    assert '--label org.opencontainers.image.revision="${DOOPS_SOURCE_COMMIT}"' in script
    assert '--label org.opencontainers.image.version="${DOOPS_RELEASE_TAG}"' in script
    assert "DOOPS_CONSOLE_ALLOWED_GATEWAYS=https://doops.invalid" in script
    assert 'fetch("http://127.0.0.1:3000")' in script
    assert 'docker push "${WEB_IMAGE}"' in script
    assert '[[ "${DOOPS_PUBLISH_LATEST:-}" == "1" && "${DOOPS_RELEASE_TAG}" != "latest" ]]' in script
    assert 'latest_image="${DOOPS_WEB_IMAGE_REPOSITORY}:latest"' in script
    assert 'docker push "${latest_image}"' in script

    assert 'bash backend/scripts/build-web-image.sh' in ci
    assert 'DOOPS_PUBLISH_LATEST: "0"' in ci
    assert "Promote verified latest aliases" in ci
    assert 'DOOPS_SOURCE_COMMIT: ${{ needs.source.outputs.commit }}' in ci
    assert 'DOOPS_RELEASE_TAG: ${{ needs.source.outputs.tag }}' in ci
    assert 'git merge-base --is-ancestor' in ci
