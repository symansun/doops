package server

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestBuiltinSkillPackagesEmbedDiskIndexAndReset locks the SF-01 contract: the
// builtin inspection skills are real, embedded directory packages; seeding
// materializes them under <dbDir>/skills/<slug>/ while SQLite keeps only a
// lightweight index; editing SKILL.md on disk is visible on the next read; and
// reset_system restores the whole package including scripts.
func TestBuiltinSkillPackagesEmbedDiskIndexAndReset(t *testing.T) {
	f := monitorEngineFixture(t)

	// 0. The embed pattern must carry underscore-prefixed fixtures too
	// (//go:embed all:skills_builtin); _embedcheck is not a real Skill package.
	if _, err := fs.ReadFile(builtinSkillPackagesFS, "skills_builtin/_embedcheck/README.md"); err != nil {
		t.Fatalf("expected embedded _embedcheck fixture: %v", err)
	}

	// 1. Every builtin spec loads its real package from the embed.
	var vmHost *defaultSystemSkillSpec
	for i := range builtinSystemSkills {
		spec := &builtinSystemSkills[i]
		skillMD, _, err := loadBuiltinSkillPackage(*spec)
		if err != nil {
			t.Fatalf("load builtin package %s: %v", spec.Slug, err)
		}
		if !strings.HasPrefix(strings.TrimSpace(skillMD), "---") {
			t.Fatalf("builtin package %s SKILL.md missing frontmatter", spec.Slug)
		}
		if spec.Slug == "inspect-vm-host" {
			vmHost = spec
		}
	}
	if vmHost == nil {
		t.Fatal("inspect-vm-host spec missing")
	}
	builtinMD, builtinFiles, err := loadBuiltinSkillPackage(*vmHost)
	if err != nil {
		t.Fatalf("load vm-host package: %v", err)
	}
	entryPath := vmHost.Entrypoint
	entryContent := ""
	if entryPath != "" {
		found := false
		for _, file := range builtinFiles {
			if file.Path == entryPath {
				entryContent = file.Content
				found = true
			}
		}
		if !found {
			t.Fatalf("entrypoint %q not attached to package (files=%+v)", entryPath, builtinFiles)
		}
	}

	// 2. Seeding materializes the package and keeps SQLite a lightweight index.
	vmDir := filepath.Join(f.store.dbDir, "skills", "inspect-vm-host")
	mdPath := filepath.Join(vmDir, "SKILL.md")
	diskMD, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("expected builtin SKILL.md materialized at %s: %v", mdPath, err)
	}
	if string(diskMD) != builtinMD {
		t.Fatalf("materialized SKILL.md does not match embedded package")
	}
	var scriptPath string
	if entryPath != "" {
		scriptPath = filepath.Join(vmDir, filepath.FromSlash(entryPath))
		diskScript, err := os.ReadFile(scriptPath)
		if err != nil {
			t.Fatalf("expected builtin entrypoint materialized at %s: %v", scriptPath, err)
		}
		if string(diskScript) != entryContent {
			t.Fatalf("materialized entrypoint does not match embedded package")
		}
	}
	var dbMD, filesJSON, storageDir string
	var fileCount int
	if err := f.store.db.QueryRowContext(context.Background(),
		`SELECT skill_md, files_json, file_count, storage_dir FROM platform_skills WHERE slug=?`,
		"inspect-vm-host").Scan(&dbMD, &filesJSON, &fileCount, &storageDir); err != nil {
		t.Fatalf("query builtin index row: %v", err)
	}
	if dbMD != "" || filesJSON != "[]" {
		t.Fatalf("expected lightweight SQLite index, got skill_md=%q files_json=%q", dbMD, filesJSON)
	}
	if storageDir != "skills/inspect-vm-host" || fileCount != len(builtinFiles) {
		t.Fatalf("unexpected storage index storage_dir=%q file_count=%d", storageDir, fileCount)
	}

	// 3. Editing SKILL.md on disk is reflected on the next read (acceptance red line 1).
	const marker = "SF-01-DISK-EDIT-MARKER"
	if err := os.WriteFile(mdPath, []byte(builtinMD+marker), 0600); err != nil {
		t.Fatalf("edit disk SKILL.md: %v", err)
	}
	rec, err := f.store.GetPlatformSkill(context.Background(), TokenAuth{UserID: "system"}, "inspect-vm-host")
	if err != nil {
		t.Fatalf("GetPlatformSkill after disk edit: %v", err)
	}
	if !strings.Contains(rec.SkillMD, marker) {
		t.Fatalf("expected disk edit reflected in skill read, got %q", rec.SkillMD)
	}
	detailRaw := f.http(t, f.admin, "GET", "/v1/skills/inspect-vm-host", "", 200)
	if !strings.Contains(string(detailRaw), marker) {
		t.Fatalf("expected disk edit reflected in HTTP detail, got %s", string(detailRaw))
	}

	// 4. reset_system restores the full builtin package, including scripts.
	f.http(t, f.admin, "POST", "/v1/skills", `{"action":"upsert","slug":"inspect-vm-host","skill_md":"# 管理员定制正文"}`, 200)
	if entryPath != "" {
		if _, err := os.Stat(scriptPath); !os.IsNotExist(err) {
			t.Fatalf("expected upsert to replace the package without scripts, err=%v", err)
		}
	}
	f.http(t, f.admin, "POST", "/v1/skills", `{"action":"reset_system","slug":"inspect-vm-host"}`, 200)
	resetMD, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read SKILL.md after reset: %v", err)
	}
	if string(resetMD) != builtinMD {
		t.Fatalf("expected reset to restore builtin SKILL.md")
	}
	if entryPath != "" {
		resetScript, err := os.ReadFile(scriptPath)
		if err != nil {
			t.Fatalf("expected reset to restore builtin scripts: %v", err)
		}
		if string(resetScript) != entryContent {
			t.Fatalf("expected reset to restore builtin script content")
		}
	}
}
// TestBuiltinSkillDispatchIsPackageDriven locks the SF-02 / D9 contract: neither
// the builtin spec nor the platform skill record may carry a builtin_strategy /
// strategy_id side channel. Dispatch is derived purely from the Skill package
// (entrypoint + exec_mode), and all eight builtin capabilities stay executable:
// the six script-backed packages dispatch to their entrypoint script, the two
// AI-driven packages dispatch to the agent prompt path.
func TestBuiltinSkillDispatchIsPackageDriven(t *testing.T) {
	requireNoStrategySideChannel(t, reflect.TypeOf(defaultSystemSkillSpec{}))
	requireNoStrategySideChannel(t, reflect.TypeOf(platformSkillRecord{}))

	specType := reflect.TypeOf(defaultSystemSkillSpec{})
	if _, ok := specType.FieldByName("Entrypoint"); !ok {
		t.Fatal("defaultSystemSkillSpec must declare the package Entrypoint")
	}
	recType := reflect.TypeOf(platformSkillRecord{})
	for _, name := range []string{"Entrypoint", "ExecMode"} {
		if _, ok := recType.FieldByName(name); !ok {
			t.Fatalf("platformSkillRecord must declare %s", name)
		}
	}

	if len(builtinSystemSkills) != 8 {
		t.Fatalf("expected 8 builtin capabilities, got %d", len(builtinSystemSkills))
	}
	var scriptSeen, agentSeen, autoSeen int
	for i := range builtinSystemSkills {
		spec := builtinSystemSkills[i]
		md, files, err := loadBuiltinSkillPackage(spec)
		if err != nil {
			t.Fatalf("load builtin package %s: %v", spec.Slug, err)
		}
		rec := platformSkillRecord{Slug: spec.Slug, Name: spec.Name, Category: spec.Category, SkillMD: md, Files: files}
		rec.Entrypoint = resolveSkillEntrypoint(rec.SkillMD, rec.Files)
		rec.ExecMode = resolveSkillExecMode(rec.SkillMD, rec.Entrypoint)
		if rec.ExecMode == skillExecModeAuto {
			autoSeen++
		}
		if rec.Entrypoint != spec.Entrypoint {
			t.Fatalf("%s entrypoint = %q, want declared %q", spec.Slug, rec.Entrypoint, spec.Entrypoint)
		}
		mode := resolveSkillDispatchMode(rec, false)
		if spec.Entrypoint != "" {
			scriptSeen++
			if mode != skillExecModeScript {
				t.Fatalf("%s must dispatch to script, got %q", spec.Slug, mode)
			}
			if got := findSkillExecutableScript(rec); got != spec.Entrypoint {
				t.Fatalf("%s runnable script = %q, want %q", spec.Slug, got, spec.Entrypoint)
			}
			if mode := resolveSkillDispatchMode(rec, true); mode != skillExecModeAgent {
				t.Fatalf("%s with a custom action must defer to the agent path, got %q", spec.Slug, mode)
			}
		} else {
			agentSeen++
			if mode != skillExecModeAgent {
				t.Fatalf("%s without entrypoint must dispatch to agent, got %q", spec.Slug, mode)
			}
			if got := findSkillExecutableScript(rec); got != "" {
				t.Fatalf("%s must not advertise a runnable script, got %q", spec.Slug, got)
			}
		}
	}
	if scriptSeen != 6 || agentSeen != 2 || autoSeen != 8 {
		t.Fatalf("unexpected dispatch split: script=%d agent=%d auto=%d", scriptSeen, agentSeen, autoSeen)
	}

	// exec_mode: script without a real entrypoint must degrade to the agent path.
	degraded := platformSkillRecord{Slug: "degraded", SkillMD: "---\nname: x\ndescription: y\nexec_mode: script\n---\n", ExecMode: skillExecModeScript}
	if mode := resolveSkillDispatchMode(degraded, false); mode != skillExecModeAgent {
		t.Fatalf("script without entrypoint must degrade to agent, got %q", mode)
	}

	// An explicit exec_mode: agent keeps an entrypoint-bearing Skill on the AI path.
	agentDeclared := platformSkillRecord{Slug: "inspect-vm-host", Entrypoint: "scripts/inspect.sh", ExecMode: skillExecModeAgent}
	if mode := resolveSkillDispatchMode(agentDeclared, false); mode != skillExecModeAgent {
		t.Fatalf("explicit exec_mode agent must stay agent, got %q", mode)
	}

	// shellSingleQuote must keep hostile slugs inert.
	if got := shellSingleQuote("a'b"); got != `'a'\''b'` {
		t.Fatalf("unexpected shellSingleQuote: %s", got)
	}
}

func requireNoStrategySideChannel(t *testing.T, typ reflect.Type) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Name == "DriverMode" || f.Name == "StrategyID" {
			t.Fatalf("%s still declares the removed strategy field %s", typ.Name(), f.Name)
		}
		tag := strings.ToLower(f.Tag.Get("json"))
		if strings.Contains(tag, "driver_mode") || strings.Contains(tag, "strategy_id") {
			t.Fatalf("%s field %s still exposes dead json tag %q", typ.Name(), f.Name, tag)
		}
	}
}
