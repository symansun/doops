import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
AGENT_RULES = ROOT / "AGENTS.md"
BACKEND = ROOT / "backend"
CLI = BACKEND / "skills" / "doops-cli" / "cli" / "doops" / "main.go"
CREDENTIAL_CLI = BACKEND / "skills" / "doops-cli" / "cli" / "doops" / "credential_commands.go"
SKILL = BACKEND / "skills" / "SKILL.md"
DEPLOYMENT_DOC = BACKEND / "docs" / "NATURAL_LANGUAGE_DEPLOYMENT.md"
CICD_DOC = BACKEND / "docs" / "AGENT_NATIVE_CICD.md"
CREDENTIAL_DOC = BACKEND / "docs" / "CREDENTIAL_RESOURCES.md"
CI_GUIDE = ROOT / "components" / "cicd-guide.tsx"


def test_retired_registry_deployment_entrypoints_are_absent():
    retired_paths = (
        ROOT / "deploy" / "environments.yaml",
        BACKEND / "agent" / "internal" / "server" / "environment_registry_v3.go",
        BACKEND / "skills" / "doops-cli" / "cli" / "doops" / "workflow.go",
        BACKEND / "agent" / "skills" / "doops-cicd",
        BACKEND / "agent" / "skills" / "pipeline" / "SKILL.md",
    )

    assert all(not path.exists() for path in retired_paths)

    runtime_source = "\n".join(
        path.read_text(encoding="utf-8")
        for root in (
            BACKEND / "agent" / "internal" / "server",
            BACKEND / "skills" / "doops-cli" / "cli" / "doops",
        )
        for path in root.glob("*.go")
    )
    assert "EnvironmentRegistry" not in runtime_source
    assert 'case "cicd"' not in runtime_source
    assert "runCICDCommand" not in runtime_source


def test_v3_release_contract_is_explicit_natural_language_apply():
    cli = CLI.read_text(encoding="utf-8")
    skill = SKILL.read_text(encoding="utf-8")
    deployment_doc = DEPLOYMENT_DOC.read_text(encoding="utf-8")
    ci_guide = CI_GUIDE.read_text(encoding="utf-8")
    system_prompt = (BACKEND / "agent" / "skills" / "system_prompt.md").read_text(encoding="utf-8")
    k8s_skill = (BACKEND / "agent" / "skills" / "k8s" / "SKILL.md").read_text(encoding="utf-8")

    assert 'cliContractVersion = "doops.sh/cli-contract/v3"' in cli
    assert 'BoolVar(&apply, "apply"' in cli
    assert 'arguments["operation"] = "apply"' in cli
    assert "doops ask --apply" in skill
    assert "target" in deployment_doc
    assert "服务范围" in deployment_doc
    assert "镜像版本" in deployment_doc
    assert "变更原因" in deployment_doc
    assert "验收条件" in deployment_doc
    assert not re.search(r"doops add(?:(?!doops login).)*--token", ci_guide, re.DOTALL)
    assert ci_guide.count("doops login") == 3
    assert ci_guide.count('--token "$DOOPS_API_KEY"') == 3
    assert "--apply" in ci_guide
    assert "dry-run" not in ci_guide
    assert "mcp_doops_k8s" in system_prompt
    assert "set-image" in system_prompt
    assert "运行契约完全未变" in system_prompt
    assert "不得用 `set-image` 跳过迁移" in system_prompt
    assert "shell 中的 Kubernetes" in system_prompt
    assert "不得用 shell 执行 kubectl mutation" in k8s_skill
    assert "strategic merge" in k8s_skill
    assert "command、args、env" in k8s_skill
    assert "运行契约发生变化时必须使用任务明确授权的 Helm 资产收敛" in k8s_skill
    assert "运行契约完全未变的纯镜像发布" in deployment_doc


def test_ordinary_exec_does_not_block_explicit_helm_operations():
    dispatcher = (BACKEND / "agent" / "internal" / "dispatcher" / "dispatcher.go").read_text(
        encoding="utf-8"
    )
    deployment_doc = DEPLOYMENT_DOC.read_text(encoding="utf-8")

    blacklist = dispatcher.split("blacklist:", 1)[1].split("naturalLangRe:", 1)[0]
    assert r"\bhelm\b.*\b(upgrade|install|rollback|uninstall)\b" not in blacklist
    assert "IsAgentHelmMutation" in dispatcher
    assert "普通前台 `exec`" in deployment_doc
    assert "不得把 Helm 误判为危险 shell 命令" in deployment_doc
    assert "后台调用必须明确拒绝" in deployment_doc


def test_cicd_and_credential_guides_match_the_runtime_contract():
    cli = CLI.read_text(encoding="utf-8")
    credential_cli = CREDENTIAL_CLI.read_text(encoding="utf-8")
    skill = SKILL.read_text(encoding="utf-8")
    cicd_doc = CICD_DOC.read_text(encoding="utf-8")
    credential_doc = CREDENTIAL_DOC.read_text(encoding="utf-8")
    api_doc = (BACKEND / "docs" / "DOOPS_API_GUIDE.md").read_text(encoding="utf-8")
    tunnel_doc = (BACKEND / "docs" / "DOOPS_GATEWAY_TUNNEL.md").read_text(encoding="utf-8")

    assert "doops ask --apply" in cicd_doc
    assert "--credential <registry-credential-name>" in cicd_doc
    assert "credential verify" in credential_doc
    assert "credential verify" in credential_cli
    assert "docs/AGENT_NATIVE_CICD.md" in skill
    assert "docs/CREDENTIAL_RESOURCES.md" in skill
    assert "backend/skills/SKILL.md" in skill

    active_contract = "\n".join((skill, cicd_doc, credential_doc, api_doc, tunnel_doc))
    assert "目标由环境注册表解析" not in active_contract
    assert "公共 Agent 不接收凭据 payload" not in active_contract
    assert "凭据 payload 不进入 prompt、Agent" not in active_contract
    assert "CICDReport；Gateway" not in active_contract
    assert "内部凭据物化调用" in active_contract
    assert "fingerprint/expiry" not in active_contract


def test_repository_agent_rules_guard_release_and_credential_boundaries():
    rules = AGENT_RULES.read_text(encoding="utf-8")

    assert rules.startswith("---\nname: doops-development-contract\n")
    assert "doops -session <s> ask --target <t> --apply" in rules
    assert "Promotion must require at least one current active grant" in rules
    assert "Historical or revoked grants" in rules
    assert "The only supported installed CLI path is `~/.local/bin/doops`" in rules
    assert "backend/scripts/install.sh --version <tag>" in rules
    assert "backend/docs/AGENT_NATIVE_CICD.md" in rules
    assert "backend/docs/CREDENTIAL_RESOURCES.md" in rules
    assert "browser testing" in rules
    assert "online synthetic credential" in rules
