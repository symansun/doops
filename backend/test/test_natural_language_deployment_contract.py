import re
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[2]
AGENT_RULES = ROOT / "AGENTS.md"
BACKEND = ROOT / "backend"
CNB = ROOT / ".cnb.yml"
CLI = BACKEND / "cli" / "cli" / "doops" / "main.go"
CREDENTIAL_CLI = BACKEND / "cli" / "cli" / "doops" / "credential_commands.go"
CREDENTIAL_PREFLIGHT = (
    BACKEND / "cli" / "cli" / "doops" / "credential_preflight.go"
)
SKILL = BACKEND / "skills" / "SKILL.md"
DEPLOYMENT_DOC = BACKEND / "docs" / "NATURAL_LANGUAGE_DEPLOYMENT.md"
CICD_DOC = DEPLOYMENT_DOC
CREDENTIAL_DOC = BACKEND / "docs" / "CREDENTIAL_RESOURCES.md"
CI_GUIDE = ROOT / "components" / "cicd-guide.tsx"


def test_v3_release_contract_is_explicit_natural_language_apply():
    cli = CLI.read_text(encoding="utf-8")
    ci_guide = CI_GUIDE.read_text(encoding="utf-8")
    system_prompt = (BACKEND / "agent" / "skills" / "system_prompt.md").read_text(encoding="utf-8")
    k8s_skill = (BACKEND / "agent" / "skills" / "k8s" / "SKILL.md").read_text(encoding="utf-8")
    handler_ws = (BACKEND / "agent" / "internal" / "server" / "handler_ws.go").read_text(
        encoding="utf-8"
    )
    helm_mcp = (
        BACKEND / "agent" / "internal" / "server" / "k8s_mcp_stdio.go"
    ).read_text(encoding="utf-8")
    cnb = CNB.read_text(encoding="utf-8")
    cnb_config = yaml.safe_load(cnb)
    main_pipeline = cnb.split("\nmaster:", 1)[0]
    fallback_pipeline = cnb.split("\n$:", 1)[1]
    release_branches = [
        branch
        for branch, events in cnb_config.items()
        if isinstance(events, dict) and "api_trigger_deploy_agent" in events
    ]
    deploy_pipelines = cnb_config["main"]["api_trigger_deploy_agent"]

    assert 'cliContractVersion = "doops.sh/cli-contract/v3"' in cli
    assert 'BoolVar(&apply, "apply"' in cli
    assert 'arguments["operation"] = "apply"' in cli
    assert not re.search(r"doops add(?:(?!doops (?:-session [^\n]+ )?login).)*--token", ci_guide, re.DOTALL)
    assert len(re.findall(r"doops -session [^\n]+ login", ci_guide)) == 3
    assert ci_guide.count("--token-stdin") == 3
    assert '--token "$DOOPS_API_KEY"' not in ci_guide
    assert "session.token" not in ci_guide
    assert "--apply" in ci_guide
    assert ci_guide.count("--credential") >= 4
    assert ci_guide.count("<IMAGE_REPOSITORY>@sha256:<IMAGE_DIGEST>") == 3
    assert "<IMAGE_VERSION>" not in ci_guide
    assert "curlimages/curl" not in ci_guide
    assert "image: alpine:3.22@sha256:<PINNED_ALPINE_DIGEST>" in ci_guide
    assert "backend/scripts/install.sh" in ci_guide
    assert "dry-run" not in ci_guide
    assert "mcp_doops_helm" in system_prompt
    assert "`session_id` 由运行时绑定" in system_prompt
    assert "target 只是路由标签" in system_prompt
    assert "Current DoOps session_id" not in handler_ws
    assert not re.search(r"mcp_doops_k8s(?!_read)", system_prompt)
    assert "set-image" not in system_prompt
    assert "包括只更新镜像" in system_prompt
    assert "`value_files`" in system_prompt
    assert "`value_files` 是可选" in system_prompt
    assert "已明确资产中的事实再粘贴到 prompt" in system_prompt
    assert "不得询问是否启用 dry-run" in system_prompt
    assert "Chart schema" in system_prompt
    assert "image.tag=release-YYYYMMDD" not in system_prompt
    assert '"value_files": map[string]interface{}' in helm_mcp
    assert '"values": map[string]interface{}' not in helm_mcp
    assert "shell 中的 Kubernetes" in system_prompt
    assert "不得用 shell 执行 kubectl mutation" in k8s_skill
    assert "api_trigger_deploy_agent" in main_pipeline
    assert "api_trigger_deploy_agent" not in fallback_pipeline
    assert "api_trigger_bootstrap_release_runner" not in cnb
    assert release_branches == ["main"]
    assert len(deploy_pipelines) == 1
    assert deploy_pipelines[0]["imports"] == [
        "https://cnb.cool/aiedulab/zhiyong/secret/-/blob/main/"
        "zhiyong-deploy-secrets.yml"
    ]
    deploy_script = deploy_pipelines[0]["stages"][1]["script"]
    prepare_script = deploy_pipelines[0]["stages"][0]["script"]
    assert ': "${DOOPS_SESSION:?DOOPS_SESSION is required}"' in prepare_script
    assert '-session "${DOOPS_SESSION}" login' in prepare_script
    assert '-session "${DOOPS_SESSION}" targets' in prepare_script
    assert "grep -Eq" not in deploy_script
    assert "validate_simple_name" in deploy_script
    assert "validate_single_line" in deploy_script
    assert 'test "${#image_digest}" -eq 64' in deploy_script
    assert 'DOOPS_HELM_ASSET must be an explicit https:// chart URL' in deploy_script
    assert '"https://doops.l8ai.cn"' in cnb
    assert '-session "${DOOPS_SESSION}" push' not in cnb
    assert "--src backend" not in cnb
    assert "使用 chart ${DOOPS_HELM_ASSET}" in cnb
    assert "--src ." not in main_pipeline
    assert '-session "${DOOPS_SESSION}" ask' in cnb
    assert "--apply" in cnb
    assert "--token-stdin" in cnb
    assert '--token "${DOOPS_GATEWAY_TOKEN}"' not in cnb
    assert "auto-harness-secret" not in cnb
    assert 'test "${CNB_BRANCH}" = "main"' in cnb
    assert "DOOPS_IMAGE" in cnb
    assert "@sha256:" in cnb
    assert "deploy/environments.yaml" not in cnb
    assert "doops cicd" not in cnb
    assert "execute-doops-cicd" not in cnb
    assert "106.54.197.139:42222" not in cnb
    assert "不存在直接 Kubernetes 镜像 mutation 例外" in k8s_skill
    assert "add-image-pull-secret" not in k8s_skill
    assert "command、args、env" in k8s_skill
    assert "任务指定的 chart" in k8s_skill
    assert "`value_files` 可选" in k8s_skill
    assert "缺少 `confirm=true` 的调用试探" in k8s_skill
    assert "image.tag=release-YYYYMMDD" not in k8s_skill
    assert "明确的 release、namespace、`value_files`" not in k8s_skill
    assert "Secret、ServiceAccount" in k8s_skill
    assert "validate_simple_name" in cnb
    assert '*[!A-Za-z0-9._-]*' in cnb
    assert 'test "${#image_digest}" -eq 64' in cnb


def test_release_guidance_does_not_build_images_on_business_targets():
    # Retired build commands are covered by test_stale_contracts.py.
    for name in ("image-build", "shell"):
        text = (BACKEND / "agent" / "skills" / name / "SKILL.md").read_text()
        metadata = yaml.safe_load(text.split("---", 2)[1])
        assert metadata["name"] == name
        assert metadata["description"]
        assert metadata["requires"] == []


def test_apply_does_not_depend_on_workspace_commit_evidence():
    handler_ws = (BACKEND / "agent" / "internal" / "server" / "handler_ws.go").read_text(
        encoding="utf-8"
    )
    workspace_upload = (
        BACKEND / "agent" / "internal" / "server" / "workspace_upload.go"
    ).read_text(encoding="utf-8")
    cli = CLI.read_text(encoding="utf-8")

    assert "doagent_apply_workspace_commit_required" not in handler_ws
    assert "validateWorkspaceCommitBinding" not in workspace_upload
    assert "validateBareWorkspaceCommitBinding" not in workspace_upload
    assert "workspace_commit" not in cli


def test_ordinary_exec_does_not_block_explicit_helm_operations():
    dispatcher = (BACKEND / "agent" / "internal" / "dispatcher" / "dispatcher.go").read_text(
        encoding="utf-8"
    )
    blacklist = dispatcher.split("var blockRules", 1)[1].split("type BlockedError", 1)[0]
    assert r"\bhelm\b.*\b(upgrade|install|rollback|uninstall)\b" not in blacklist
    assert "IsHelmMutation" in dispatcher


def test_cicd_and_credential_guides_match_the_runtime_contract():
    credential_cli = CREDENTIAL_CLI.read_text(encoding="utf-8")
    credential_preflight = CREDENTIAL_PREFLIGHT.read_text(encoding="utf-8")

    # Doc links are checked by test_doc_links.py; retired phrases by test_stale_contracts.py.
    assert "credential verify" in credential_cli
    assert "credential preflight" in credential_cli
    assert ".agent/doops-credentials.yaml" in credential_preflight
    assert "DevelopmentCredentialRequirements" in credential_preflight
    assert "KnownFields(true)" in credential_preflight
    assert "credentialHasActiveTarget" in credential_preflight


def test_repository_agent_rules_guard_release_and_credential_boundaries():
    rules = AGENT_RULES.read_text(encoding="utf-8")

    assert rules.startswith("---\nname: doops-development-contract\n")
    assert "doops -session <s> ask --target <t> --apply" in rules
    assert "Promotion must require at least one current active grant" in rules
    assert "Historical or revoked grants" in rules
    assert "The only supported installed CLI path is `~/.local/bin/doops`" in rules
    assert "backend/scripts/install.sh --version <tag>" in rules
    assert "CLI's own Go parser" in rules
    assert "backend/docs/NATURAL_LANGUAGE_DEPLOYMENT.md" in rules
    assert "backend/docs/CREDENTIAL_RESOURCES.md" in rules
    assert "browser testing" in rules
    assert "online synthetic credential" in rules
    assert ".agent/doops-credentials.yaml" in rules
    assert "credential preflight" in rules
