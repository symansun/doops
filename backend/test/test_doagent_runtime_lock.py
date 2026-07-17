from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
RUNTIME = ROOT / "runtime-versions.env"
CI = ROOT.parent / ".cnb.yml"
DOCKERFILES = (
    ROOT / "Dockerfile",
    ROOT / "Dockerfile.base.light",
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
    assert runtime["DO_AGENT_VERSION"] == "0.2.21"
    assert runtime["DO_AGENT_IMAGE"].endswith(
        f"@{runtime['DO_AGENT_INDEX_DIGEST']}"
    )

    ci = CI.read_text()
    assert '--build-arg DO_AGENT_REVISION="${DO_AGENT_REVISION}"' in ci
    assert (
        'test "${ACTUAL_DOAGENT_REVISION}" = "${DO_AGENT_REVISION}"'
        in ci
    )

    for dockerfile in DOCKERFILES:
        text = dockerfile.read_text()
        assert "ARG DO_AGENT_REVISION" in text
        assert (
            'LABEL org.l8ai.doagent.revision="${DO_AGENT_REVISION}"'
            in text
        )
