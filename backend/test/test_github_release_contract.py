"""Lock the GitHub tag-release contract.

The public repository line publishes CLI/Gateway release assets from a tag through
`.github/workflows/release.yml`; CNB keeps the production image and deployment path.
These checks assert structure: gated by CI, immutable, verified after publishing, and
built with the same builder identity the release scripts require. Wording is not locked.
"""

from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ROOT / ".github" / "workflows"
RELEASE = WORKFLOWS / "release.yml"
CI = WORKFLOWS / "ci.yml"
BUILD_CLI = ROOT / "backend" / "scripts" / "build-cli.sh"
VERIFY = ROOT / "backend" / "scripts" / "verify-published-cli-release.sh"
INSTALL = ROOT / "backend" / "scripts" / "install.sh"

# builder identity backend/scripts/build-cli.sh --all insists on
RELEASE_BUILDER = "1.25.14"

# artifacts backend/scripts/build-cli.sh --all publishes
RELEASE_ASSETS = (
    "checksums.txt",
    "doops-darwin-amd64",
    "doops-darwin-arm64",
    "doops-gateway-linux-amd64",
    "doops-linux-amd64",
    "doops-linux-arm64",
)


def load_workflow(path: Path):
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    return data


def triggers(data):
    # PyYAML resolves the YAML 1.1 key on to True.
    return data[True] if True in data else data["on"]


def steps_of(job):
    return job.get("steps", [])


def run_text(job):
    return "\n".join(str(step.get("run", "")) for step in steps_of(job))


def test_release_workflow_only_runs_for_tags_or_manual_dispatch():
    release = load_workflow(RELEASE)
    on = triggers(release)
    assert set(on) == {"push", "workflow_dispatch"}
    assert on["push"] == {"tags": ["v*"]}
    inputs = on["workflow_dispatch"]["inputs"]
    assert inputs["tag"]["required"] is True
    assert inputs["publish"]["type"] == "boolean"
    assert inputs["publish"]["default"] is False
    assert release["permissions"] == {"contents": "read"}
    assert release["concurrency"]["cancel-in-progress"] is False


def test_release_is_gated_by_the_reusable_ci_workflow():
    release = load_workflow(RELEASE)
    gate = release["jobs"]["gate"]
    assert gate["uses"] == "./.github/workflows/ci.yml"
    # A rejected tag fails in prepare; do not spend a full test run on it.
    needs = gate["needs"]
    assert needs == "prepare" or needs == ["prepare"]
    ci = load_workflow(CI)
    assert "workflow_call" in triggers(ci)
    # gitleaks scans full history on tag/dispatch events; only branch pushes and
    # pull requests carry a change range, so the release gate skips it.
    condition = ci["jobs"]["secret-scan"]["if"]
    assert "refs/tags/" in condition
    assert "github.event_name == 'pull_request'" in condition
    # The release contract tests themselves gate the release workflow.
    assert "contract-tests" in ci["jobs"]


def test_publish_requires_a_green_ci_and_stays_least_privileged():
    release = load_workflow(RELEASE)
    jobs = release["jobs"]
    assert jobs["build"]["needs"] == ["prepare", "gate"]
    publish = jobs["publish"]
    assert publish["needs"] == ["prepare", "build"]
    assert publish["permissions"] == {"contents": "write"}
    assert publish["if"] == "needs.prepare.outputs.publish == 'true'"
    runs = run_text(publish)
    assert "gh release view" in runs  # published releases are immutable
    assert "gh release create" in runs
    assert "--prerelease" in runs
    verify = jobs["verify"]
    assert verify["needs"] == ["prepare", "publish"]
    assert verify["permissions"] == {"contents": "read", "attestations": "read"}


def test_release_series_needs_explicit_maintainer_authorization():
    prepare = load_workflow(RELEASE)["jobs"]["prepare"]
    resolve = next(step for step in steps_of(prepare) if step.get("id") == "resolve")
    allowed = resolve["env"]["ALLOWED_SERIES"]
    assert "vars.DOOPS_RELEASE_SERIES" in allowed  # maintainer-only repository variable
    assert "0.2" in allowed  # current public series, v0.2.x patch increments only
    runs = run_text(prepare)
    assert "series=" in runs
    assert "not authorized" in runs


def test_prepare_rejects_off_main_and_already_published_tags():
    runs = run_text(load_workflow(RELEASE)["jobs"]["prepare"])
    assert "refs/tags/" in runs
    assert "merge-base --is-ancestor" in runs
    assert "gh release view" in runs
    assert "GITHUB_OUTPUT" in runs


def test_build_uses_the_pinned_release_builder_and_verified_assets():
    build = load_workflow(RELEASE)["jobs"]["build"]
    setup_go = next(step for step in steps_of(build) if step.get("uses", "").startswith("actions/setup-go@"))
    assert setup_go["with"]["go-version"] == RELEASE_BUILDER
    runs = run_text(build)
    assert "backend/scripts/build-cli.sh --all" in runs
    assert "sha256sum --check --strict checksums.txt" in runs
    assert "doops.sh/cli-contract/v3" in runs
    upload = next(step for step in steps_of(build) if step.get("uses", "").startswith("actions/upload-artifact@"))
    assert upload["with"]["name"] == "release-assets"
    assert upload["with"]["if-no-files-found"] == "error"
    assert build["permissions"]["id-token"] == "write"
    assert any(step.get("uses", "").startswith("actions/attest-build-provenance@") for step in steps_of(build))


def test_verify_job_rechecks_the_published_release_and_installer():
    runs = run_text(load_workflow(RELEASE)["jobs"]["verify"])
    assert "DOOPS_RELEASE_ASSET_BASE=" in runs
    assert "backend/scripts/verify-published-cli-release.sh" in runs
    assert "gh attestation verify" in runs
    assert "backend/scripts/install.sh" in runs


def test_release_asset_names_match_the_build_script_contract():
    build_script = BUILD_CLI.read_text(encoding="utf-8")
    assert "checksums.txt" in build_script
    assert "doops-gateway" in build_script
    assert "doops-darwin" in build_script
    assert "doops-linux" in build_script
    assert "linux/amd64" in build_script
    for name in RELEASE_ASSETS:
        if name == "checksums.txt":
            continue
        platform = name[len("doops-"):]
        if platform.startswith("gateway-"):
            platform = platform[len("gateway-"):]
        assert platform in build_script, name


def test_install_and_verify_scripts_accept_the_github_release_mirror():
    verify = VERIFY.read_text(encoding="utf-8")
    install = INSTALL.read_text(encoding="utf-8")
    for text in (verify, install):
        assert "DOOPS_RELEASE_ASSET_BASE" in text
    # CNB stays the default publisher; GitHub is an explicit, https-only mirror.
    assert "https://cnb.cool/" in verify
    assert "l8ai/ai/doops.sh" in verify
    assert "/-/releases/download" in verify
    assert "https://cnb.cool/l8ai/ai/doops.sh/-/releases/download" in install
    assert "DOOPS_RELEASE_ASSET_BASE:-" in install
    assert "DOOPS_RELEASE_ASSET_BASE:-" in verify
