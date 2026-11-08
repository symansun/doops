"""The ask sandbox cannot Read the operator docs outside its workspace."""

import re
from pathlib import Path


BACKEND = Path(__file__).resolve().parents[1]


def test_skill_contains_the_complete_documented_host_probe():
    skill = (BACKEND / "agent/skills/shell/SKILL.md").read_text()
    guide = (BACKEND / "docs/PHYSICAL_HOST_ACCESS.md").read_text()
    pattern = r"^```bash\n(.*?)^```"
    skill_blocks = re.findall(pattern, skill, re.MULTILINE | re.DOTALL)
    guide_blocks = re.findall(pattern, guide, re.MULTILINE | re.DOTALL)
    assert len(skill_blocks) == 1, "Skill.load_prompt must provide the full probe"
    assert guide_blocks, "The operator guide must provide an executable probe"
    assert skill_blocks[0] == guide_blocks[0], "CLI and ask must use the same probe"
