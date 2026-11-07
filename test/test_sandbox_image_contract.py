from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def test_base_image_requires_external_doagent_image():
    base = read("Dockerfile.base")
    light = read("Dockerfile.base.light")

    for dockerfile in (base, light):
        assert "ARG DO_AGENT_IMAGE=doagent:latest" in dockerfile
        assert "/usr/local/bin/do-agent --help" in dockerfile


def test_app_image_is_layered_on_base_image():
    dockerfile = read("agent/Dockerfile")

    assert "ARG DOOPS_AGENT_BASE_IMAGE=doops-base:latest" in dockerfile
    assert "CGO_ENABLED=0 go build -o /app/doops-agent ./cmd/agent" in dockerfile
    assert "COPY --from=builder /app/agent/skills /app/skills" in dockerfile
    assert 'ENTRYPOINT ["/app/sandbox-entrypoint.sh"]' in dockerfile


def test_sandbox_runtime_contract_has_no_legacy_agent_surface():
    combined = "\n".join(
        [
            read("agent/Dockerfile"),
            read("agent/sandbox-entrypoint.sh"),
        ]
    )

    forbidden = [
        "llm.example.com:8443",
        "/sse",
        "-password",
        "--password",
        "opencode",
        "OpenCode",
        "lab/webide",
    ]
    for needle in forbidden:
        assert needle not in combined


def test_sandbox_entrypoint_starts_doagent_buildkit_and_gateway():
    entrypoint = read("agent/sandbox-entrypoint.sh")

    assert 'DO_AGENT_PORT="${DO_AGENT_PORT:-9000}"' in entrypoint
    assert "/usr/local/bin/do-agent acp-http --port" in entrypoint
    assert "buildkitd --containerd-worker=false" in entrypoint
    assert "tini -s -- /app/doops-agent" in entrypoint
    assert "https://llm.example.com/v1" in entrypoint


def test_lightweight_base_contract_keeps_runtime_tools_without_webide_surface():
    dockerfile = read("Dockerfile.base.light")

    assert "kubectl version --client=true" in dockerfile
    assert "buildctl --version" in dockerfile
    assert "apt-get purge -y --auto-remove openssh-server openssh-client rsync sudo" in dockerfile
    assert "rm -f /usr/local/bin/start-webide.sh /entrypoint.sh /usr/bin/entrypoint.sh /init.sh" in dockerfile
