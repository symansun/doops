from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
CI = ROOT.parent / ".cnb.yml"
RUNTIME = ROOT / "runtime-versions.env"
IMAGE_BUILD = ROOT / "scripts" / "build-agent-image.sh"
BASE_IMAGE_BUILD = ROOT / "scripts" / "build-agent-base-image.sh"
LIGHT_BASE = ROOT / "Dockerfile.base.light"
SKILL = ROOT / "skills" / "SKILL.md"
USAGE_GUIDE = ROOT / "docs" / "DOOPS_USAGE_GUIDE.md"
DOCKERFILES = (
    ROOT / "Dockerfile",
    LIGHT_BASE,
    ROOT / "agent" / "Dockerfile",
    ROOT / "agent" / "Dockerfile.sandbox",
)


def test_doagent_runtime_lock_includes_exact_source_revision():
    runtime = dict(
        line.split("=", 1)
        for line in RUNTIME.read_text().splitlines()
        if line and not line.startswith("#")
    )
    revision = runtime["DO_AGENT_REVISION"]

    assert len(revision) == 40
    assert runtime["DO_AGENT_VERSION"] == "0.2.26"
    assert runtime["DO_AGENT_IMAGE"].endswith(
        f"@{runtime['DO_AGENT_INDEX_DIGEST']}"
    )

    image_build = IMAGE_BUILD.read_text()
    assert '--build-arg DO_AGENT_REVISION="${DO_AGENT_REVISION}"' in image_build
    assert (
        'test "${ACTUAL_DOAGENT_REVISION}" = "${DO_AGENT_REVISION}"'
        in image_build
    )

    for dockerfile in DOCKERFILES:
        text = dockerfile.read_text()
        assert "ARG DO_AGENT_REVISION" in text
        assert (
            'LABEL org.l8ai.doagent.revision="${DO_AGENT_REVISION}"'
            in text
        )


def test_agent_runtime_keeps_ssh_client_and_rejects_ssh_server():
    base = LIGHT_BASE.read_text()

    assert "openssh-client" in base
    assert "openssh-server openssh-client" not in base
    assert "ssh -V" in base
    assert "! command -v sshd" in base

    for dockerfile in (
        ROOT / "Dockerfile",
        ROOT / "agent" / "Dockerfile",
        ROOT / "agent" / "Dockerfile.sandbox",
    ):
        text = dockerfile.read_text()
        assert "ssh -V" in text
        assert "! command -v sshd" in text

    image_build = IMAGE_BUILD.read_text()
    assert "--entrypoint /usr/bin/ssh" in image_build
    assert "ssh client" in image_build
    assert "command -v sshd" in image_build
    assert "DOOPS_AGENT_BASE_IMAGE_OVERRIDE" in image_build

    base_image_build = BASE_IMAGE_BUILD.read_text()
    assert "--entrypoint /usr/bin/ssh" in base_image_build
    assert "command -v sshd" in base_image_build
    assert "DOOPS_BASE_IMAGE_OUTPUT" in base_image_build

    ci = CI.read_text()
    assert "build-agent-base-image.sh" in ci
    assert "backend/.ci-agent-base-image" in ci
    assert "backend/runtime-versions.env" in ci


def test_doops_ssh_uses_structured_ssh_commands():
    skill = SKILL.read_text()
    usage = USAGE_GUIDE.read_text()

    assert "doops ssh" in skill
    assert "结构化" in skill and "嵌套引号" in skill
    assert "Run nested SSH safely" in usage
    assert "必须使用结构化入口" in usage
    assert "SSH 只用于首次安装、自恢复 agent" not in skill
    assert "才可通过前台 `doops exec` 执行 SSH 恢复" not in skill
