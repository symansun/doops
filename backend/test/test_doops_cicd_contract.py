from pathlib import Path


ROOT = Path(__file__).parents[1]
SKILL = ROOT / "agent" / "skills" / "doops-cicd" / "SKILL.md"
DOCKERFILE = ROOT / "Dockerfile"
CNB_PIPELINE = ROOT.parent / ".cnb.yml"
ACTIVE_CONTRACT_DOCS = (
    ROOT / "agent" / "DESIGN.md",
    ROOT / "skills" / "SKILL.md",
    ROOT / "docs" / "SEMANTIC_CICD.md",
    ROOT / "docs" / "BUILD_DEPLOY_SOP.md",
    ROOT / "docs" / "AGENT_NATIVE_CICD.md",
    ROOT / "docs" / "DOOPS_API_GUIDE.md",
    ROOT / "docs" / "DOOPS_GATEWAY_TUNNEL.md",
    ROOT / "docs" / "design" / "AI_Ops_System_Design.md",
    ROOT / "skills" / "doops-cli" / "cli" / "doops" / "main.go",
    ROOT / "skills" / "doops-cli" / "cli" / "doops" / "workflow.go",
    ROOT.parent / "docs" / "credential-plan-convergence.md",
)
RELEASE_PROTOCOL_DOCS = (
    ROOT / "docs" / "DOOPS_API_GUIDE.md",
    ROOT / "docs" / "DOOPS_GATEWAY_TUNNEL.md",
)


def test_cicd_skill_uses_gateway_bound_v3_single_service_contract():
    text = SKILL.read_text()
    assert "deploy/environments.yaml" in text
    assert "`environment`" in text
    assert "`service`" in text
    assert "Gateway" in text
    assert "CredentialRun" in text
    assert "doops.reconciliation-context/v2" in text
    assert "遍历其他服务" in text
    assert "fallback" in text
    for removed in (
        "EnvironmentDefinition",
        "DeploymentTemplate",
        "SemanticRelease",
        "ServiceRelease",
        "DeploymentRun",
        "doops_plan",
    ):
        assert removed not in text


def test_active_cicd_docs_only_describe_gateway_bound_v3_registry():
    text = "\n".join(path.read_text() for path in ACTIVE_CONTRACT_DOCS)

    for required in (
        "deploy/environments.yaml",
        "doops.sh/v3",
        "EnvironmentRegistry",
        "environment",
        "service",
        "doops.sh/credential-plan/v2",
        "doops.reconciliation-context/v2",
        "doops.sh/cicd-contract/v3",
    ):
        assert required in text

    for removed in (
        "v2 DeploymentTemplate |",
        "uses `DeploymentTemplate`",
        "deploy/workflows/oilan-agent-bootstrap",
        "configurationSource",
        "--set releaseId",
        "-set reason=",
        "声明式 CI/CD workflow 入口",
        "run 先同步当前仓库",
        "DeploymentTemplate -> doops push",
        "EnvironmentRegistry -> doops push",
        "CI/CD push 完成后",
        "自然语言部署 API 对接模式",
        "使用 `doops push` 或 workspace upload 工具上传仓库内容",
        "不提供发布专用权限、metadata 或 workspace admission",
    ):
        assert removed not in text


def test_each_release_protocol_doc_declares_gateway_bound_v3_contract():
    required = (
        "doops.sh/v3",
        "EnvironmentRegistry",
        "/v1/credentials/prepare",
        "CredentialRun",
        "doops.sh/credential-plan/v2",
        "doops.sh/cicd-contract/v3",
        "doops.reconciliation-context/v2",
        "426 Upgrade Required",
    )
    forbidden = (
        "发布脚本和 skill 的标准路径必须是 Git HTTP",
        "标准 CLI 和发布脚本必须使用 Git HTTP",
        "执行构建并更新 deployment",
        "适用于首次适配环境",
        "生成部署脚本等探索型任务",
        "标准部署过程中",
    )
    for path in RELEASE_PROTOCOL_DOCS:
        text = path.read_text()
        for phrase in required:
            assert phrase in text, f"{path.name} missing {phrase}"
        for phrase in forbidden:
            assert phrase not in text, f"{path.name} still contains {phrase}"


def test_ci_image_contains_agent_and_gateway_from_the_same_revision():
    dockerfile = DOCKERFILE.read_text()
    pipeline = CNB_PIPELINE.read_text()

    assert "go build -o /app/doops-agent ./cmd/agent" in dockerfile
    assert "go build -o /app/doops-gateway ." in dockerfile
    assert "COPY --from=builder /app/doops-gateway /app/doops-gateway" in dockerfile
    assert "/app/doops-gateway -help" in dockerfile
    assert "docker run --rm --entrypoint /app/doops-gateway" in pipeline
