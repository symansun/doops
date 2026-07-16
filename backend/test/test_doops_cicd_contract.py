import platform
import stat
import subprocess
from pathlib import Path

import yaml


ROOT = Path(__file__).parents[1]
SKILL = ROOT / "agent" / "skills" / "doops-cicd"
WORKFLOW = ROOT / "deploy" / "workflows" / "oilan-agent-bootstrap.yaml"
CLI_BIN = ROOT / "skills" / "doops-cli" / "bin"


def test_doops_cicd_skill_is_generic_and_agent_native():
    text = (SKILL / "SKILL.md").read_text()
    assert "doops.sh/v3" in text
    assert "doops.sh/v2" in text
    assert "doagent 原生多 Agent 引擎" in text
    assert "BuildDefinition" in text
    assert "EnvironmentDefinition" in text
    assert "ServiceDefinition" in text
    assert "EnvironmentService" in text
    assert "DeploymentReview" in text
    assert "SemanticRelease" in text
    assert "ServiceRelease" in text
    assert "deploy/deploy.yaml" in text
    assert "deploy/environments/<environment>/environment.yaml" in text
    assert "不得把 v3 声明降级" in text
    assert "回滚基线" in text
    assert "pipeline" in text
    assert "toolRef" in text
    for forbidden in (
        "DeploymentPlan",
        "ReconciliationResult",
        "doops_plan",
        "reconcilehelmrelease",
    ):
        assert forbidden not in text


def test_v3_split_contract_is_chinese_and_has_no_workflow_index():
    declaration = (SKILL / "references" / "deployment-template.md").read_text()
    execution = (SKILL / "references" / "execution-contract.md").read_text()
    for kind in (
        "BuildDefinition",
        "EnvironmentDefinition",
        "ServiceDefinition",
        "EnvironmentService",
    ):
        assert kind in declaration
    assert "deploy/environments.yaml" not in declaration
    assert "deploy/workflows/" not in declaration
    assert "同一 apiVersion" in (SKILL / "SKILL.md").read_text()
    assert "只执行一次" in execution
    assert "rollback baseline" in execution


def test_existing_workflow_and_execution_contract_define_one_result():
    workflow = yaml.safe_load(WORKFLOW.read_text())
    contract = (SKILL / "references" / "execution-contract.md").read_text()

    assert workflow["apiVersion"] == "doops.sh/v2"
    assert workflow["kind"] == "DeploymentTemplate"
    assert workflow["spec"]["parameters"]["releaseId"]["required"] is True
    assert "${inputs.releaseId}" in str(workflow["spec"]["release"]["source"])
    assert workflow["spec"]["configurationSource"] == "backend/deploy/environments.yaml"
    assert "DeploymentRun" in contract
    assert "mutationCount" in contract
    assert "evidence" in contract
    assert "fallback" in contract.lower()
    assert "exactly one shared-release convergence" in contract
    assert "zero deployment executor" in contract
    assert "invocations" in contract
    assert "unselected service" in contract
    assert "toolRef" in contract
    assert "toolCall" in contract
    assert "turnId" in contract
    assert "Gateway" in contract
def test_system_prompt_routes_templates_to_doops_cicd():
    prompt = (ROOT / "agent" / "skills" / "system_prompt.md").read_text()
    assert "DeploymentTemplate" in prompt
    assert "SemanticRelease" in prompt
    assert "ServiceRelease" in prompt
    assert "doops-cicd" in prompt
    assert "DeploymentPlan" not in prompt
    assert "ReconciliationResult" not in prompt


def test_prebuilt_cli_exposes_only_agent_native_cicd_run():
    system = platform.system().lower()
    machine = {"x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[
        platform.machine().lower()
    ]
    binary = CLI_BIN / f"doops-{system}-{machine}"

    help_result = subprocess.run(
        [str(binary), "-help"],
        text=True,
        capture_output=True,
        check=False,
    )
    help_text = help_result.stdout + help_result.stderr
    assert help_result.returncode == 0
    assert "workflow 入口 (run)" in help_text
    assert "Agent 原生声明式 CI/CD 示例" in help_text
    assert "兼容适配" not in help_text
    assert "--allow-mutate" not in help_text
    assert "cicd lint" not in help_text
    assert "cicd plan" not in help_text

    retired_result = subprocess.run(
        [str(binary), "cicd", "plan"],
        text=True,
        capture_output=True,
        check=False,
    )
    retired_text = retired_result.stdout + retired_result.stderr
    assert retired_result.returncode != 0
    assert "only `cicd run` is supported" in retired_text


def test_installer_replaces_execute_only_cli_with_launchable_mode(tmp_path):
    home = tmp_path / "home"
    project = tmp_path / "project"
    tool_bin = tmp_path / "bin"
    cli = home / ".local" / "bin" / "doops"
    cli.parent.mkdir(parents=True)
    project.mkdir()
    tool_bin.mkdir()
    sha256sum = tool_bin / "sha256sum"
    sha256sum.write_text("#!/bin/sh\nexec /usr/bin/shasum -a 256 \"$@\"\n")
    sha256sum.chmod(0o755)
    cli.write_text("stale")
    cli.chmod(0o111)

    result = subprocess.run(
        [
            "bash",
            str(ROOT / "scripts" / "install.sh"),
            "--project",
            str(project),
        ],
        text=True,
        capture_output=True,
        check=False,
        env={"HOME": str(home), "PATH": f"{tool_bin}:/usr/bin:/bin:/usr/sbin:/sbin"},
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert stat.S_IMODE(cli.stat().st_mode) == 0o755
    help_result = subprocess.run(
        [str(cli), "-help"],
        text=True,
        capture_output=True,
        check=False,
    )
    assert help_result.returncode == 0


def test_installer_rejects_directory_cli_target(tmp_path):
    home = tmp_path / "home"
    project = tmp_path / "project"
    cli = home / ".local" / "bin" / "doops"
    cli.mkdir(parents=True)
    project.mkdir()

    result = subprocess.run(
        [
            "bash",
            str(ROOT / "scripts" / "install.sh"),
            "--project",
            str(project),
        ],
        text=True,
        capture_output=True,
        check=False,
        env={"HOME": str(home), "PATH": "/usr/bin:/bin:/usr/sbin:/sbin"},
    )
    assert result.returncode != 0
    assert "CLI install path is a directory" in result.stderr
    assert list(cli.iterdir()) == []
