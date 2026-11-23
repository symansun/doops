package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
