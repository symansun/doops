import os
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
WORKFLOWS = ROOT.parent / ".github" / "workflows"
SCRIPT = ROOT / "scripts" / "build-web-image.sh"


def test_web_image_release_builds_smokes_and_pushes_immutable_image():
    script = SCRIPT.read_text()

    assert ': "${DOOPS_IMAGE_REGISTRY:?DOOPS_IMAGE_REGISTRY is required}"' in script
    assert ': "${DOOPS_IMAGE_REPOSITORY:?DOOPS_IMAGE_REPOSITORY is required}"' in script
    assert ': "${DOOPS_SOURCE_COMMIT:?DOOPS_SOURCE_COMMIT is required}"' in script
    assert "DOOPS_IMAGE_TAG is required" in script
    assert 'WEB_IMAGE="${DOOPS_IMAGE_REGISTRY}/${DOOPS_IMAGE_REPOSITORY}/web:${DOOPS_IMAGE_TAG}"' in script
    assert '-f "${REPO_ROOT}/../Dockerfile.web"' in script
    assert '--label org.opencontainers.image.revision="${DOOPS_SOURCE_COMMIT}"' in script
    assert '--label org.opencontainers.image.version="${DOOPS_IMAGE_TAG}"' in script
    assert "DOOPS_CONSOLE_ALLOWED_GATEWAYS=https://doops.invalid" in script
    assert 'fetch("http://127.0.0.1:3000")' in script
    assert 'docker push "${WEB_IMAGE}"' in script
    assert '[[ "${DOOPS_PUBLISH_LATEST:-}" == "1" && "${DOOPS_IMAGE_TAG}" != "latest" ]]' in script
    assert 'latest_image="${DOOPS_IMAGE_REGISTRY}/${DOOPS_IMAGE_REPOSITORY}/web:latest"' in script
    assert 'docker push "${latest_image}"' in script


def test_web_image_release_is_not_built_by_ci():
    # Production images are built by the operator with explicit inputs; CI only
    # tests the repository and publishes the CLI/Gateway/Skill release assets.
    for workflow in sorted(WORKFLOWS.glob("*.yml")):
        text = workflow.read_text(encoding="utf-8")
        assert "build-web-image.sh" not in text, workflow


def test_web_image_release_fails_closed_without_operator_inputs():
    base_env = {
        key: value
        for key, value in os.environ.items()
        if key
        not in {
            "DOOPS_IMAGE_TAG",
            "DOOPS_IMAGE_REGISTRY",
            "DOOPS_IMAGE_REPOSITORY",
            "DOOPS_SOURCE_COMMIT",
        }
    }

    missing_tag = subprocess.run(
        ["bash", str(SCRIPT)], capture_output=True, text=True, env=base_env
    )
    assert missing_tag.returncode != 0
    assert "DOOPS_IMAGE_TAG is required" in missing_tag.stderr

    missing_registry = subprocess.run(
        ["bash", str(SCRIPT)],
        capture_output=True,
        text=True,
        env={**base_env, "DOOPS_IMAGE_TAG": "v1.2.3"},
    )
    assert missing_registry.returncode != 0
    assert "DOOPS_IMAGE_REGISTRY is required" in missing_registry.stderr
