package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const skillsInspectTool = "doops_skills_inspect"

type agentLoadedSkillInfo struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"` // dir | file
	FileCount   int    `json:"file_count"`
	SizeBytes   int64  `json:"size_bytes"`
	UpdatedAtMs int64  `json:"updated_at_ms"`
}

type skillSyncParams struct {
	SessionID string              `json:"session_id"`
	Slug      string              `json:"slug"`
	SkillMD   string              `json:"skill_md"`
	Files     []platformSkillFile `json:"files,omitempty"`
	Remove    bool                `json:"remove,omitempty"`
}

func agentSkillsRoot() string {
	if custom := strings.TrimSpace(os.Getenv("DOOPS_AGENT_SKILLS_ROOT")); custom != "" {
		return custom
	}
	return "/root/.agent/skills"
}

func (gw *Gateway) handleToolSkillSync(call toolInvocation) {
	reqID, writeJSON := call.reqID, call.writeJSON
	var params skillSyncParams
	if err := json.Unmarshal(call.args, &params); err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid skill sync params"))
		return
	}
	slug := strings.ToLower(strings.TrimSpace(params.Slug))
	if !skillSlugPattern.MatchString(slug) {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid skill slug"))
		return
	}

	root := agentSkillsRoot()
	if err := os.MkdirAll(root, 0755); err != nil {
		writeJSON(buildToolErrorResponse(reqID, "skills_root_unavailable: "+err.Error()))
		return
	}
	skillDir := filepath.Join(root, slug)
	relCheck, err := filepath.Rel(root, skillDir)
	if err != nil || relCheck != slug {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid skill path"))
		return
	}

	if params.Remove {
		if err := os.RemoveAll(skillDir); err != nil {
			writeJSON(buildToolErrorResponse(reqID, "skill_remove_failed: "+err.Error()))
			return
		}
		out, _ := json.Marshal(map[string]any{"slug": slug, "removed": true, "dir": skillDir})
		writeJSON(buildSuccessResponse(reqID, string(out)))
		return
	}

	if len(strings.TrimSpace(params.SkillMD)) == 0 || len(params.SkillMD) > maxSkillMarkdownBytes {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid skill_md length"))
		return
	}
	cleanFiles, filesBytes, err := sanitizeSkillFiles(params.Files)
	if err != nil || len(params.SkillMD)+filesBytes > maxSkillTotalBytes {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid skill attachments"))
		return
	}

	tmpDir, err := os.MkdirTemp(root, ".skill-sync-"+slug+"-*")
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, "skill_stage_failed: "+err.Error()))
		return
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(params.SkillMD), 0644); err != nil {
		writeJSON(buildToolErrorResponse(reqID, "skill_write_failed: "+err.Error()))
		return
	}

	written := 1
	for _, f := range cleanFiles {
		dst := filepath.Join(tmpDir, filepath.FromSlash(f.Path))
		rel, err := filepath.Rel(tmpDir, dst)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			writeJSON(buildErrorResponse(reqID, -32602, "invalid skill file path"))
			return
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			writeJSON(buildToolErrorResponse(reqID, "skill_mkdir_failed: "+err.Error()))
			return
		}
		mode := os.FileMode(0644)
		if strings.HasPrefix(f.Path, "scripts/") || strings.HasSuffix(f.Path, ".sh") || strings.HasSuffix(f.Path, ".py") {
			mode = 0755
		}
		if err := os.WriteFile(dst, []byte(f.Content), mode); err != nil {
			writeJSON(buildToolErrorResponse(reqID, "skill_file_write_failed: "+err.Error()))
			return
		}
		written++
	}

	if err := os.RemoveAll(skillDir); err != nil {
		writeJSON(buildToolErrorResponse(reqID, "skill_replace_failed: "+err.Error()))
		return
	}
	if err := os.Rename(tmpDir, skillDir); err != nil {
		writeJSON(buildToolErrorResponse(reqID, "skill_commit_failed: "+err.Error()))
		return
	}

	out, _ := json.Marshal(map[string]any{
		"slug":          slug,
		"dir":           skillDir,
		"files_written": written,
		"detail":        fmt.Sprintf("synced %d files to %s", written, skillDir),
	})
	writeJSON(buildSuccessResponse(reqID, string(out)))
}

func (gw *Gateway) handleToolSkillsInspect(call toolInvocation) {
	reqID, writeJSON := call.reqID, call.writeJSON
	root := agentSkillsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			out, _ := json.Marshal(map[string]any{"root": root, "skills": []agentLoadedSkillInfo{}})
			writeJSON(buildSuccessResponse(reqID, string(out)))
			return
		}
		writeJSON(buildToolErrorResponse(reqID, "skills_inspect_failed: "+err.Error()))
		return
	}

	skills := make([]agentLoadedSkillInfo, 0, len(entries))
	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, ".") || name == "system_prompt.md" {
			continue
		}
		fullPath := filepath.Join(root, name)
		if ent.IsDir() {
			mdBytes, _ := os.ReadFile(filepath.Join(fullPath, "SKILL.md"))
			fmName, fmDesc := parseSkillFrontmatter(string(mdBytes))
			if fmName == "" {
				fmName = name
			}
			var totalSize int64
			fileCount := 0
			var latestMod int64
			_ = filepath.WalkDir(fullPath, func(p string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil || d.IsDir() {
					return nil
				}
				fileCount++
				if info, err := d.Info(); err == nil {
					totalSize += info.Size()
					if ms := info.ModTime().UnixMilli(); ms > latestMod {
						latestMod = ms
					}
				}
				return nil
			})
			skills = append(skills, agentLoadedSkillInfo{
				Slug:        name,
				Name:        fmName,
				Description: fmDesc,
				Kind:        "dir",
				FileCount:   fileCount,
				SizeBytes:   totalSize,
				UpdatedAtMs: latestMod,
			})
		} else if strings.HasSuffix(name, ".md") {
			slug := strings.TrimSuffix(name, ".md")
			mdBytes, _ := os.ReadFile(fullPath)
			fmName, fmDesc := parseSkillFrontmatter(string(mdBytes))
			if fmName == "" {
				fmName = slug
			}
			var sz, mod int64
			if info, err := ent.Info(); err == nil {
				sz = info.Size()
				mod = info.ModTime().UnixMilli()
			}
			skills = append(skills, agentLoadedSkillInfo{
				Slug:        slug,
				Name:        fmName,
				Description: fmDesc,
				Kind:        "file",
				FileCount:   1,
				SizeBytes:   sz,
				UpdatedAtMs: mod,
			})
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Slug < skills[j].Slug })
	out, _ := json.Marshal(map[string]any{
		"root":   root,
		"skills": skills,
	})
	writeJSON(buildSuccessResponse(reqID, string(out)))
}
