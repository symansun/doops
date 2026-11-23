# _embedcheck

This directory is a build-time fixture owned by the Go (agent runtime) change set.
It is intentionally underscore-prefixed so it is not a real Skill package and is
never seeded as a system skill. It exists so `//go:embed all:skills_builtin` has
content to embed and `TestBuiltinSkillPackagesEmbedDiskIndexAndReset` can prove
the embed pipeline includes underscore-prefixed directories.

Real builtin Skill packages live in sibling <slug>/ directories and are owned by
the content change set.
