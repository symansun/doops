"""Contract guards for P0 CI/CD quality gates, caching, and CNB alignment."""

import re
import subprocess
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[2]
CI = ROOT / ".github" / "workflows" / "ci.yml"
CNB = ROOT / ".cnb.yml"
AGENT_DOCKERFILE = ROOT / "backend" / "Dockerfile"
PREFLIGHT = ROOT / "backend" / "scripts" / "release-preflight.sh"


def _load_yaml(path: Path):
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _steps_of(job):
    return job.get("steps", [])


def _run_text(job):
    return "\n".join(str(step.get("run", "")) for step in _steps_of(job))


def test_ci_gofmt_vet_and_cli_cross_compile_cover_all_go_modules():
    ci = _load_yaml(CI)
    go_job = ci["jobs"]["test"]
    runs = _run_text(go_job)
    for mod in ("backend/agent", "backend/gateway", "backend/cli"):
        assert mod in runs, f"Go CI missing module {mod}"
    assert "gofmt -l ." in runs
    assert "go vet ./..." in runs
    assert "GOOS=darwin GOARCH=arm64 go build" in runs
    assert "GOOS=windows GOARCH=amd64 go build" in runs


def test_ci_jobs_define_timeouts_and_caches():
    ci = _load_yaml(CI)
    for name, job in ci["jobs"].items():
        timeout = job.get("timeout-minutes")
        assert isinstance(timeout, int) and timeout > 0, f"job {name} missing timeout-minutes"

    web = ci["jobs"]["web"]
    web_caches = [
        step.get("with", {}).get("path", "")
        for step in _steps_of(web)
        if str(step.get("uses", "")).startswith("actions/cache@")
    ]
    assert "${{ steps.pnpm-store.outputs.dir }}" in web_caches, f"web job missing pnpm store cache: {web_caches}"
    assert ".next/cache" in web_caches, f"web job missing Next.js cache: {web_caches}"
    assert "runner." not in str(web.get("env", "")), "job-level env must not reference runner.* context"

    skills = ci["jobs"]["skills-packages"]
    assert any(
        str(step.get("uses", "")).startswith("docker/setup-buildx-action@")
        for step in _steps_of(skills)
    ), "skills-packages job missing Buildx setup"
    skills_runs = _run_text(skills)
    assert "--cache-from type=gha,scope=sb01-inspect-test" in skills_runs
    assert "--cache-to type=gha,mode=max,scope=sb01-inspect-test" in skills_runs


def test_cnb_pipeline_uses_corepack_runs_dosql_and_drops_dead_master():
    raw = CNB.read_text(encoding="utf-8")
    data = _load_yaml(CNB)
    assert "main" in data
    assert "$" in data
    assert "master" not in data, "stale master branch pipeline should remain removed"
    assert "npm i -g pnpm@" not in raw, ".cnb.yml must not hardcode a drifting pnpm version"
    assert "require('./package.json').packageManager" in raw
    assert "corepack enable" in raw
    assert "pnpm test:dosql" in raw
    assert "gofmt -l ." in raw


def test_release_preflight_script_syntax_and_cli_contract():
    subprocess.run(["bash", "-n", str(PREFLIGHT)], check=True)
    help_res = subprocess.run(
        ["bash", str(PREFLIGHT), "--help"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        timeout=15,
    )
    assert help_res.returncode == 0, help_res.stderr
    assert "--prev-tag" in help_res.stdout and "--next-tag" in help_res.stdout

    bad_res = subprocess.run(
        ["bash", str(PREFLIGHT), "--unknown-flag"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        timeout=15,
    )
    assert bad_res.returncode != 0
    assert "unknown option" in bad_res.stderr


def test_agent_dockerfile_skill_checks_match_bundled_agent_skills():
    text = AGENT_DOCKERFILE.read_text(encoding="utf-8")
    referenced = sorted(set(re.findall(r"/app/skills/([A-Za-z0-9._/-]+)", text)))
    assert referenced, "expected backend/Dockerfile to smoke-check bundled /app/skills/* scripts"
    missing = [rel for rel in referenced if not (ROOT / "skills" / "agent" / rel).is_file()]
    assert not missing, f"backend/Dockerfile references missing skills/agent files: {missing}"
