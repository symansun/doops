package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	skillSyncTool           = "doops_skill_sync"
	maxSkillMarkdownBytes   = 512 * 1024
	maxSkillFileBytes       = 256 * 1024
	maxSkillTotalBytes      = 1024 * 1024
	maxSkillAttachmentFiles = 16
)

var (
	skillSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

type platformSkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type platformSkillRecord struct {
	Slug        string              `json:"slug"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Scope       string              `json:"scope"`       // system | custom
	Category    string              `json:"category"`    // inspection | security | ops
	DriverMode  string              `json:"driver_mode"` // builtin_strategy | agent_prompt
	StrategyID  string              `json:"strategy_id,omitempty"`
	Enabled     bool                `json:"enabled"`
	SkillMD     string              `json:"skill_md,omitempty"`
	Files       []platformSkillFile `json:"files,omitempty"`
	FileCount   int                 `json:"file_count"`
	SHA256      string              `json:"sha256"`
	OwnerID     string              `json:"owner_id"`
	OwnerName   string              `json:"owner_name"`
	CreatedAtMs int64               `json:"created_at_ms"`
	UpdatedAtMs int64               `json:"updated_at_ms"`
	LastRunAtMs int64               `json:"last_run_at_ms,omitempty"`
	RunCount    int                 `json:"run_count"`
	CanEdit     bool                `json:"can_edit"`
	CanDelete   bool                `json:"can_delete"`
}

func computeSkillSHA256(skillMD string, files []platformSkillFile) string {
	h := sha256.New()
	h.Write([]byte(skillMD))
	h.Write([]byte{0})
	for _, f := range files {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		h.Write([]byte(f.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func parseSkillFrontmatter(md string) (name, desc string) {
	trimmed := strings.TrimSpace(md)
	if !strings.HasPrefix(trimmed, "---") {
		return "", ""
	}
	rest := strings.TrimPrefix(trimmed, "---")
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", ""
	}
	header := rest[:idx]
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k == "name" && name == "" {
			name = v
		} else if k == "description" && desc == "" {
			desc = v
		}
	}
	return name, desc
}

func validateSkillRelPath(raw string) (string, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if cleaned == "" || strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "\x00") {
		return "", fmt.Errorf("invalid_skill_file_path")
	}
	norm := path.Clean(cleaned)
	if norm == "." || norm == ".." || strings.HasPrefix(norm, "../") || strings.Contains(norm, "/../") || strings.EqualFold(norm, "SKILL.md") {
		return "", fmt.Errorf("invalid_skill_file_path")
	}
	if len(norm) > 180 {
		return "", fmt.Errorf("invalid_skill_file_path")
	}
	return norm, nil
}

func sanitizeSkillFiles(files []platformSkillFile) ([]platformSkillFile, int, error) {
	if len(files) > maxSkillAttachmentFiles {
		return nil, 0, fmt.Errorf("too_many_skill_files")
	}
	seen := make(map[string]bool, len(files))
	out := make([]platformSkillFile, 0, len(files))
	totalBytes := 0
	for _, f := range files {
		p, err := validateSkillRelPath(f.Path)
		if err != nil {
			return nil, 0, err
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		if len(f.Content) > maxSkillFileBytes {
			return nil, 0, fmt.Errorf("skill_file_too_large")
		}
		totalBytes += len(f.Content)
		out = append(out, platformSkillFile{Path: p, Content: f.Content})
	}
	return out, totalBytes, nil
}

// SeedBuiltinSystemSkills indexes every builtin Skill package and materializes
// its real directory under <dbDir>/skills/<slug>/. An existing package on disk
// is authoritative and is never overwritten here, so an admin edit survives a
// restart; only a first-time (or missing) package is written from the embed.
func (s *GatewayStore) SeedBuiltinSystemSkills(ctx context.Context) error {
	nowMs := time.Now().UnixMilli()
	for _, spec := range builtinSystemSkills {
		skillMD, files, err := loadBuiltinSkillPackage(spec)
		if err != nil {
			return err
		}
		storageDir, effectiveMD, effectiveFiles, err := s.materializeBuiltinSkillPackage(ctx, spec, skillMD, files)
		if err != nil {
			return err
		}
		sha := computeSkillSHA256(effectiveMD, effectiveFiles)
		fileCount := len(effectiveFiles)
		dbSkillMD := effectiveMD
		dbFilesJSON := "[]"
		if storageDir != "" {
			// Content lives on the Gateway filesystem; SQLite keeps only the lightweight metadata index.
			dbSkillMD = ""
		} else {
			raw, _ := json.Marshal(effectiveFiles)
			dbFilesJSON = string(raw)
		}
		_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO platform_skills (
			slug, name, description, scope, category, driver_mode, strategy_id,
			enabled, skill_md, files_json, file_count, storage_dir, sha256, owner_id, owner_name,
			created_at_ms, updated_at_ms, last_run_at_ms, run_count
		) VALUES (?, ?, ?, 'system', ?, ?, ?, 1, ?, ?, ?, ?, ?, 'system', 'DoOps 系统内置', ?, ?, 0, 0)`,
			spec.Slug, spec.Name, spec.Description, spec.Category, spec.DriverMode, spec.StrategyID,
			dbSkillMD, dbFilesJSON, fileCount, storageDir, sha, nowMs, nowMs)
		if err != nil {
			return err
		}
		if storageDir != "" {
			// Rows seeded by an older build kept the full body in SQLite; fold them onto
			// the disk-backed index without touching the on-disk body chosen above.
			if _, err := s.db.ExecContext(ctx, `UPDATE platform_skills
				SET skill_md='', files_json='[]', file_count=?, storage_dir=?, sha256=?
				WHERE slug=?`, fileCount, storageDir, sha, spec.Slug); err != nil {
				return err
			}
		}
	}
	return nil
}

// materializeBuiltinSkillPackage resolves the effective content for one builtin
// package (admin-edited disk > legacy admin-edited DB body > embedded default)
// and writes it to <dbDir>/skills/<slug>/ on first use. It returns the relative
// storage dir ("" when filesystem storage is disabled) plus the content that is
// actually in effect, so the SQLite index matches what reads will return.
func (s *GatewayStore) materializeBuiltinSkillPackage(ctx context.Context, spec defaultSystemSkillSpec, skillMD string, files []platformSkillFile) (string, string, []platformSkillFile, error) {
	if s.skillStorageRoot() == "" {
		return "", skillMD, files, nil
	}
	if diskMD, diskFiles, ok := s.readSkillFilesFromDisk(spec.Slug); ok {
		return "skills/" + spec.Slug, diskMD, diskFiles, nil
	}
	effectiveMD := skillMD
	effectiveFiles := files
	if dbMD, dbFiles, ok := s.storedBuiltinSkillContent(ctx, spec.Slug); ok {
		if strings.TrimSpace(dbMD) != "" {
			effectiveMD = dbMD
		}
		if len(dbFiles) > 0 {
			effectiveFiles = dbFiles
		}
	}
	if _, err := s.writeSkillFilesToDisk(spec.Slug, effectiveMD, effectiveFiles); err != nil {
		return "", "", nil, err
	}
	return "skills/" + spec.Slug, effectiveMD, effectiveFiles, nil
}

// storedBuiltinSkillContent returns the body an older build persisted in SQLite.
// New stores keep only the disk-backed index, in which case ok is false.
func (s *GatewayStore) storedBuiltinSkillContent(ctx context.Context, slug string) (string, []platformSkillFile, bool) {
	var skillMD, filesJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT skill_md, files_json FROM platform_skills WHERE slug=?`, slug).Scan(&skillMD, &filesJSON); err != nil {
		return "", nil, false
	}
	var files []platformSkillFile
	if filesJSON != "" && filesJSON != "[]" {
		_ = json.Unmarshal([]byte(filesJSON), &files)
	}
	if strings.TrimSpace(skillMD) == "" && len(files) == 0 {
		return "", nil, false
	}
	return skillMD, files, true
}

func (s *GatewayStore) skillStorageRoot() string {
	if s.dbDir == "" || s.dbDir == "." {
		return ""
	}
	return filepath.Join(s.dbDir, "skills")
}

func (s *GatewayStore) writeSkillFilesToDisk(slug, skillMD string, files []platformSkillFile) (string, error) {
	root := s.skillStorageRoot()
	if root == "" {
		return "", nil
	}
	if !skillSlugPattern.MatchString(slug) {
		return "", errors.New("invalid skill slug")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	targetDir := filepath.Join(root, slug)
	tmpDir, err := os.MkdirTemp(root, ".stage-"+slug+"-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(skillMD), 0600); err != nil {
		return "", err
	}
	for _, f := range files {
		dst := filepath.Join(tmpDir, filepath.FromSlash(f.Path))
		rel, err := filepath.Rel(tmpDir, dst)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return "", errors.New("invalid skill attachment path")
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0600); err != nil {
			return "", err
		}
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return "", err
	}
	if err := os.Rename(tmpDir, targetDir); err != nil {
		return "", err
	}
	return "skills/" + slug, nil
}

func (s *GatewayStore) readSkillFilesFromDisk(slug string) (string, []platformSkillFile, bool) {
	root := s.skillStorageRoot()
	if root == "" || !skillSlugPattern.MatchString(slug) {
		return "", nil, false
	}
	dir := filepath.Join(root, slug)
	mdBytes, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", nil, false
	}
	files := []platformSkillFile{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		files = append(files, platformSkillFile{Path: rel, Content: string(data)})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return string(mdBytes), files, true
}

func (s *GatewayStore) ListPlatformSkills(ctx context.Context, auth TokenAuth, scopeFilter, categoryFilter string, includeBody bool) ([]platformSkillRecord, error) {
	isAdmin := auth.UserID != "" && s.UserIsPlatformAdmin(auth.UserID)
	args := []any{}
	where := []string{"1=1"}
	if scopeFilter == "system" || scopeFilter == "custom" {
		where = append(where, "scope=?")
		args = append(args, scopeFilter)
	}
	if categoryFilter != "" {
		where = append(where, "category=?")
		args = append(args, categoryFilter)
	}
	if !isAdmin {
		where = append(where, "(scope='system' OR enabled=1 OR owner_id=?)")
		args = append(args, auth.UserID)
	}
	query := `SELECT slug, name, description, scope, category, driver_mode, strategy_id,
		enabled, skill_md, files_json, file_count, storage_dir, sha256, owner_id, owner_name,
		created_at_ms, updated_at_ms, last_run_at_ms, run_count
		FROM platform_skills WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY CASE scope WHEN 'system' THEN 0 ELSE 1 END, category, slug`
	rows, err := s.readDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []platformSkillRecord{}
	for rows.Next() {
		rec, err := s.scanPlatformSkillRow(rows, auth.UserID, isAdmin, includeBody)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *GatewayStore) GetPlatformSkill(ctx context.Context, auth TokenAuth, slug string) (platformSkillRecord, error) {
	isAdmin := auth.UserID == "system" || (auth.UserID != "" && s.UserIsPlatformAdmin(auth.UserID))
	row := s.readDB().QueryRowContext(ctx, `SELECT slug, name, description, scope, category, driver_mode, strategy_id,
		enabled, skill_md, files_json, file_count, storage_dir, sha256, owner_id, owner_name,
		created_at_ms, updated_at_ms, last_run_at_ms, run_count
		FROM platform_skills WHERE slug=?`, slug)
	rec, err := s.scanPlatformSkillRow(row, auth.UserID, isAdmin, true)
	if err != nil {
		return rec, err
	}
	if !isAdmin && rec.Scope != "system" && !rec.Enabled && rec.OwnerID != auth.UserID {
		return platformSkillRecord{}, sql.ErrNoRows
	}
	return rec, nil
}

type skillRowScanner interface {
	Scan(dest ...any) error
}

func (s *GatewayStore) scanPlatformSkillRow(sc skillRowScanner, userID string, isAdmin bool, includeBody bool) (platformSkillRecord, error) {
	var (
		rec        platformSkillRecord
		enabled    int
		skillMD    string
		filesJSON  string
		fileCount  int
		storageDir string
	)
	if err := sc.Scan(
		&rec.Slug, &rec.Name, &rec.Description, &rec.Scope, &rec.Category, &rec.DriverMode, &rec.StrategyID,
		&enabled, &skillMD, &filesJSON, &fileCount, &storageDir, &rec.SHA256, &rec.OwnerID, &rec.OwnerName,
		&rec.CreatedAtMs, &rec.UpdatedAtMs, &rec.LastRunAtMs, &rec.RunCount,
	); err != nil {
		return rec, err
	}
	rec.Enabled = enabled != 0
	var legacyFiles []platformSkillFile
	if filesJSON != "" && filesJSON != "[]" {
		_ = json.Unmarshal([]byte(filesJSON), &legacyFiles)
	}
	if fileCount > 0 {
		rec.FileCount = fileCount
	} else {
		rec.FileCount = len(legacyFiles)
	}
	if includeBody {
		if diskMD, diskFiles, ok := s.readSkillFilesFromDisk(rec.Slug); ok {
			rec.SkillMD = diskMD
			rec.Files = diskFiles
			rec.FileCount = len(diskFiles)
		} else {
			rec.SkillMD = skillMD
			rec.Files = legacyFiles
		}
	}
	if rec.Scope == "system" {
		rec.CanEdit = isAdmin
		rec.CanDelete = false
	} else {
		rec.CanEdit = isAdmin || (userID != "" && rec.OwnerID == userID)
		rec.CanDelete = rec.CanEdit
	}
	return rec, nil
}

func (s *GatewayStore) UpsertPlatformSkill(ctx context.Context, rec platformSkillRecord) (platformSkillRecord, error) {
	nowMs := time.Now().UnixMilli()
	if rec.CreatedAtMs <= 0 {
		rec.CreatedAtMs = nowMs
	}
	rec.UpdatedAtMs = nowMs
	rec.SHA256 = computeSkillSHA256(rec.SkillMD, rec.Files)
	rec.FileCount = len(rec.Files)

	storageDir, diskErr := s.writeSkillFilesToDisk(rec.Slug, rec.SkillMD, rec.Files)
	dbSkillMD := rec.SkillMD
	dbFilesJSON := "[]"
	if diskErr == nil && storageDir != "" {
		// Content lives on the Gateway filesystem; SQLite keeps only the lightweight metadata index.
		dbSkillMD = ""
		dbFilesJSON = "[]"
	} else {
		raw, _ := json.Marshal(rec.Files)
		dbFilesJSON = string(raw)
	}

	enabledInt := 0
	if rec.Enabled {
		enabledInt = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO platform_skills (
		slug, name, description, scope, category, driver_mode, strategy_id,
		enabled, skill_md, files_json, file_count, storage_dir, sha256, owner_id, owner_name,
		created_at_ms, updated_at_ms, last_run_at_ms, run_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(slug) DO UPDATE SET
		name=excluded.name,
		description=excluded.description,
		category=excluded.category,
		driver_mode=excluded.driver_mode,
		strategy_id=excluded.strategy_id,
		enabled=excluded.enabled,
		skill_md=excluded.skill_md,
		files_json=excluded.files_json,
		file_count=excluded.file_count,
		storage_dir=excluded.storage_dir,
		sha256=excluded.sha256,
		updated_at_ms=excluded.updated_at_ms`,
		rec.Slug, rec.Name, rec.Description, rec.Scope, rec.Category, rec.DriverMode, rec.StrategyID,
		enabledInt, dbSkillMD, dbFilesJSON, rec.FileCount, storageDir, rec.SHA256, rec.OwnerID, rec.OwnerName,
		rec.CreatedAtMs, rec.UpdatedAtMs, rec.LastRunAtMs, rec.RunCount,
	)
	return rec, err
}

func (s *GatewayStore) TogglePlatformSkill(ctx context.Context, slug string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE platform_skills SET enabled=?, updated_at_ms=? WHERE slug=?`, v, time.Now().UnixMilli(), slug)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GatewayStore) DeletePlatformSkill(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM platform_skills WHERE slug=? AND scope='custom'`, slug)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if root := s.skillStorageRoot(); root != "" && skillSlugPattern.MatchString(slug) {
		_ = os.RemoveAll(filepath.Join(root, slug))
	}
	return nil
}

func (s *GatewayStore) RecordPlatformSkillRun(ctx context.Context, slugs []string, atMs int64) {
	if len(slugs) == 0 {
		return
	}
	if atMs <= 0 {
		atMs = time.Now().UnixMilli()
	}
	for _, slug := range slugs {
		_, _ = s.db.ExecContext(ctx, `UPDATE platform_skills SET run_count=run_count+1, last_run_at_ms=? WHERE slug=?`, atMs, slug)
	}
}

type skillMutationRequest struct {
	Action      string              `json:"action"` // upsert | toggle | delete | reset_system | sync
	Slug        string              `json:"slug"`
	Name        string              `json:"name,omitempty"`
	Description string              `json:"description,omitempty"`
	Scope       string              `json:"scope,omitempty"`
	Category    string              `json:"category,omitempty"`
	DriverMode  string              `json:"driver_mode,omitempty"`
	StrategyID  string              `json:"strategy_id,omitempty"`
	Enabled     *bool               `json:"enabled,omitempty"`
	SkillMD     string              `json:"skill_md,omitempty"`
	Files       []platformSkillFile `json:"files,omitempty"`
	Targets     []monitorTargetRef  `json:"targets,omitempty"`
}

func writeSkillHTTPError(w http.ResponseWriter, status int, code, msg string) {
	writeDoopsError(w, status, "", doopsErrorResponse{Error: code, Code: code, Message: msg})
}

func (h *GatewayHub) HandlePlatformSkills(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	subPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/skills"), "/")

	switch r.Method {
	case http.MethodGet:
		if subPath != "" {
			if !skillSlugPattern.MatchString(subPath) {
				writeSkillHTTPError(w, 400, "invalid_skill_slug", "Skill 标识仅支持小写字母、数字和连字符")
				return
			}
			rec, err := h.store.GetPlatformSkill(r.Context(), auth, subPath)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeSkillHTTPError(w, 404, "skill_not_found", "Skill 不存在或无权查看")
					return
				}
				writeSkillHTTPError(w, 503, "skill_store_unavailable", "无法读取 Skill 详情")
				return
			}
			writeJSONHTTP(w, map[string]any{"skill": rec})
			return
		}
		q := r.URL.Query()
		includeBody := q.Get("include_body") == "1" || q.Get("include_body") == "true"
		items, err := h.store.ListPlatformSkills(r.Context(), auth, strings.TrimSpace(q.Get("scope")), strings.TrimSpace(q.Get("category")), includeBody)
		if err != nil {
			writeSkillHTTPError(w, 503, "skill_store_unavailable", "无法读取 Skill 列表")
			return
		}
		resp := map[string]any{"skills": items}
		if rawTarget := strings.TrimSpace(q.Get("target")); rawTarget != "" {
			cluster, instance, ok := strings.Cut(rawTarget, "/")
			if !ok || !monitorID(cluster) || !monitorID(instance) {
				writeSkillHTTPError(w, 400, "invalid_target", "目标格式应为 cluster/instance")
				return
			}
			if !h.store.UserIsPlatformAdmin(auth.UserID) && !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
				writeSkillHTTPError(w, 403, "target_forbidden", "当前账号无权查看该目标已加载的 Skill")
				return
			}
			callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(r.Context(), auth), 20*time.Second)
			rawOut, callErr := h.RunInternalToolCall(callCtx, cluster, instance, skillsInspectTool, map[string]any{
				"session_id": "skills-inspect-" + randomHex(4),
			})
			cancel()
			if callErr != nil {
				resp["agent_error"] = trimTail(sanitizeSecretText(callErr.Error()), 200)
			} else {
				var parsed struct {
					Root   string                 `json:"root"`
					Skills []agentLoadedSkillInfo `json:"skills"`
				}
				if json.Unmarshal([]byte(strings.TrimSpace(rawOut)), &parsed) == nil {
					resp["agent_skills_root"] = parsed.Root
					resp["agent_skills"] = parsed.Skills
				}
			}
		}
		writeJSONHTTP(w, resp)

	case http.MethodPost:
		if subPath != "" {
			writeSkillHTTPError(w, 405, "method_not_allowed", "不支持对该路径 POST")
			return
		}
		var req skillMutationRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSkillTotalBytes+64*1024))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			writeSkillHTTPError(w, 400, "invalid_skill_json", "Skill 请求格式无效或超出 1 MiB 大小限制")
			return
		}
		isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
		action := strings.TrimSpace(req.Action)
		if action == "" {
			action = "upsert"
		}
		slug := strings.ToLower(strings.TrimSpace(req.Slug))

		switch action {
		case "upsert":
			if !skillSlugPattern.MatchString(slug) {
				writeSkillHTTPError(w, 400, "invalid_skill_slug", "Skill 标识（slug）必须为 1–63 位小写字母、数字或连字符")
				return
			}
			if len(strings.TrimSpace(req.SkillMD)) == 0 {
				writeSkillHTTPError(w, 400, "skill_md_required", "SKILL.md 正文不能为空")
				return
			}
			if len(req.SkillMD) > maxSkillMarkdownBytes {
				writeSkillHTTPError(w, 400, "skill_md_too_large", "SKILL.md 超过 512 KiB 限制")
				return
			}
			cleanFiles, filesBytes, err := sanitizeSkillFiles(req.Files)
			if err != nil {
				writeSkillHTTPError(w, 400, err.Error(), "Skill 附属文件路径非法或超过大小限制")
				return
			}
			if len(req.SkillMD)+filesBytes > maxSkillTotalBytes {
				writeSkillHTTPError(w, 400, "skill_total_too_large", "Skill 总大小超过 1 MiB 限制")
				return
			}

			existing, existErr := h.store.GetPlatformSkill(r.Context(), TokenAuth{UserID: "system"}, slug)
			exists := existErr == nil
			scope := "custom"
			if exists {
				scope = existing.Scope
				if scope == "system" && !isAdmin {
					writeSkillHTTPError(w, 403, "system_skill_admin_required", "仅平台管理员可修改系统 Skill")
					return
				}
				if scope == "custom" && !isAdmin && existing.OwnerID != auth.UserID {
					writeSkillHTTPError(w, 403, "skill_owner_required", "无权修改其他用户上传的自定义 Skill")
					return
				}
			} else if strings.TrimSpace(req.Scope) == "system" {
				if !isAdmin {
					writeSkillHTTPError(w, 403, "system_skill_admin_required", "仅平台管理员可创建系统 Skill")
					return
				}
				scope = "system"
			}

			fmName, fmDesc := parseSkillFrontmatter(req.SkillMD)
			name := strings.TrimSpace(req.Name)
			if name == "" {
				name = fmName
			}
			if name == "" {
				name = slug
			}
			desc := strings.TrimSpace(req.Description)
			if desc == "" {
				desc = fmDesc
			}

			category := strings.TrimSpace(req.Category)
			switch category {
			case "inspection", "security", "ops":
			default:
				if exists {
					category = existing.Category
				} else {
					category = "inspection"
				}
			}

			driverMode := strings.TrimSpace(req.DriverMode)
			strategyID := strings.TrimSpace(req.StrategyID)
			if exists && existing.DriverMode == "builtin_strategy" && driverMode == "" {
				driverMode = "builtin_strategy"
				strategyID = existing.StrategyID
			}
			if driverMode == "builtin_strategy" {
				if !validMonitorStrategies[strategyID] {
					writeSkillHTTPError(w, 400, "invalid_strategy_id", "内置策略 Skill 必须绑定有效的监控巡检策略")
					return
				}
			} else {
				driverMode = "agent_prompt"
				strategyID = ""
			}

			enabled := true
			if req.Enabled != nil {
				enabled = *req.Enabled
			} else if exists {
				enabled = existing.Enabled
			}

			ownerID := auth.UserID
			ownerName := auth.UserID
			var dbUserName string
			if h.store.readDB().QueryRowContext(r.Context(), `SELECT name FROM users WHERE id=?`, auth.UserID).Scan(&dbUserName) == nil && strings.TrimSpace(dbUserName) != "" {
				ownerName = strings.TrimSpace(dbUserName)
			}
			if exists {
				ownerID = existing.OwnerID
				ownerName = existing.OwnerName
			}

			saved, err := h.store.UpsertPlatformSkill(r.Context(), platformSkillRecord{
				Slug:        slug,
				Name:        name,
				Description: desc,
				Scope:       scope,
				Category:    category,
				DriverMode:  driverMode,
				StrategyID:  strategyID,
				Enabled:     enabled,
				SkillMD:     req.SkillMD,
				Files:       cleanFiles,
				OwnerID:     ownerID,
				OwnerName:   ownerName,
				CreatedAtMs: existing.CreatedAtMs,
				LastRunAtMs: existing.LastRunAtMs,
				RunCount:    existing.RunCount,
			})
			if err != nil {
				writeSkillHTTPError(w, 503, "skill_save_failed", "保存 Skill 失败")
				return
			}
			saved.CanEdit = true
			saved.CanDelete = saved.Scope == "custom"
			writeJSONHTTP(w, map[string]any{"skill": saved})

		case "toggle":
			if !skillSlugPattern.MatchString(slug) || req.Enabled == nil {
				writeSkillHTTPError(w, 400, "invalid_toggle_request", "请提供有效的 Skill 标识与开关状态")
				return
			}
			existing, err := h.store.GetPlatformSkill(r.Context(), TokenAuth{UserID: "system"}, slug)
			if err != nil {
				writeSkillHTTPError(w, 404, "skill_not_found", "Skill 不存在")
				return
			}
			if existing.Scope == "system" && !isAdmin {
				writeSkillHTTPError(w, 403, "system_skill_admin_required", "仅平台管理员可开关系统 Skill")
				return
			}
			if existing.Scope == "custom" && !isAdmin && existing.OwnerID != auth.UserID {
				writeSkillHTTPError(w, 403, "skill_owner_required", "无权修改其他用户的自定义 Skill")
				return
			}
			if err := h.store.TogglePlatformSkill(r.Context(), slug, *req.Enabled); err != nil {
				writeSkillHTTPError(w, 503, "skill_toggle_failed", "更新 Skill 开关失败")
				return
			}
			updated, _ := h.store.GetPlatformSkill(r.Context(), auth, slug)
			writeJSONHTTP(w, map[string]any{"skill": updated})

		case "delete":
			if !skillSlugPattern.MatchString(slug) {
				writeSkillHTTPError(w, 400, "invalid_skill_slug", "请提供有效的 Skill 标识")
				return
			}
			existing, err := h.store.GetPlatformSkill(r.Context(), TokenAuth{UserID: "system"}, slug)
			if err != nil {
				writeSkillHTTPError(w, 404, "skill_not_found", "Skill 不存在")
				return
			}
			if existing.Scope == "system" {
				writeSkillHTTPError(w, 409, "system_skill_protected", "系统内置 Skill 不允许删除，可关闭或编辑覆盖")
				return
			}
			if !isAdmin && existing.OwnerID != auth.UserID {
				writeSkillHTTPError(w, 403, "skill_owner_required", "无权删除其他用户的自定义 Skill")
				return
			}
			if err := h.store.DeletePlatformSkill(r.Context(), slug); err != nil {
				writeSkillHTTPError(w, 503, "skill_delete_failed", "删除自定义 Skill 失败")
				return
			}
			writeJSONHTTP(w, map[string]any{"deleted": slug})

		case "reset_system":
			if !isAdmin {
				writeSkillHTTPError(w, 403, "system_skill_admin_required", "仅平台管理员可重置系统 Skill")
				return
			}
			var found *defaultSystemSkillSpec
			for i := range builtinSystemSkills {
				if builtinSystemSkills[i].Slug == slug {
					found = &builtinSystemSkills[i]
					break
				}
			}
			if found == nil {
				writeSkillHTTPError(w, 404, "builtin_skill_not_found", "未找到对应的系统内置 Skill 模板")
				return
			}
			// Restore the full builtin package (SKILL.md plus scripts) from the embed,
			// not just the Markdown body.
			builtinMD, builtinFiles, err := loadBuiltinSkillPackage(*found)
			if err != nil {
				writeSkillHTTPError(w, 500, "builtin_skill_load_failed", "加载系统内置 Skill 包失败")
				return
			}
			existing, _ := h.store.GetPlatformSkill(r.Context(), TokenAuth{UserID: "system"}, slug)
			saved, err := h.store.UpsertPlatformSkill(r.Context(), platformSkillRecord{
				Slug:        found.Slug,
				Name:        found.Name,
				Description: found.Description,
				Scope:       "system",
				Category:    found.Category,
				DriverMode:  found.DriverMode,
				StrategyID:  found.StrategyID,
				Enabled:     true,
				SkillMD:     builtinMD,
				Files:       builtinFiles,
				OwnerID:     "system",
				OwnerName:   "DoOps 系统内置",
				CreatedAtMs: existing.CreatedAtMs,
				LastRunAtMs: existing.LastRunAtMs,
				RunCount:    existing.RunCount,
			})
			if err != nil {
				writeSkillHTTPError(w, 503, "skill_reset_failed", "重置系统 Skill 失败")
				return
			}
			saved.CanEdit = true
			saved.CanDelete = false
			writeJSONHTTP(w, map[string]any{"skill": saved})

		case "sync":
			results, err := h.syncSkillsToTargets(r.Context(), auth, slug, req.Targets)
			if err != nil {
				writeSkillHTTPError(w, 400, "skill_sync_failed", err.Error())
				return
			}
			writeJSONHTTP(w, map[string]any{"results": results})

		default:
			writeSkillHTTPError(w, 400, "invalid_skill_action", "不支持的 Skill 操作")
		}

	default:
		w.Header().Set("Allow", "GET, POST")
		writeSkillHTTPError(w, 405, "method_not_allowed", "仅支持 GET / POST")
	}
}

type skillSyncTargetResult struct {
	Cluster     string `json:"cluster"`
	Instance    string `json:"instance"`
	SyncedCount int    `json:"synced_count"`
	Status      string `json:"status"` // ok | offline | error
	Detail      string `json:"detail,omitempty"`
}

func (h *GatewayHub) syncSkillsToTargets(ctx context.Context, auth TokenAuth, slug string, requested []monitorTargetRef) ([]skillSyncTargetResult, error) {
	var skills []platformSkillRecord
	if slug != "" {
		rec, err := h.store.GetPlatformSkill(ctx, auth, slug)
		if err != nil {
			return nil, fmt.Errorf("skill %q not found or forbidden", slug)
		}
		skills = []platformSkillRecord{rec}
	} else {
		all, err := h.store.ListPlatformSkills(ctx, auth, "", "", true)
		if err != nil {
			return nil, err
		}
		for _, item := range all {
			if item.Enabled {
				skills = append(skills, item)
			}
		}
	}

	plan := monitorRunPlan{
		TriggeredBy: auth.UserID,
		Auth:        auth,
		ScopeMode:   "all_online",
		Targets:     requested,
	}
	if len(requested) > 0 {
		plan.ScopeMode = "targets"
	}
	targets := h.resolveMonitorPlanTargets(ctx, plan)
	results := make([]skillSyncTargetResult, 0, len(targets))

	for _, t := range targets {
		if h.getAgent(t.Cluster, t.Instance) == nil {
			results = append(results, skillSyncTargetResult{
				Cluster: t.Cluster, Instance: t.Instance, Status: "offline", Detail: "目标 Agent 当前离线",
			})
			continue
		}
		synced := 0
		var lastErr string
		for _, sk := range skills {
			if err := h.syncOneSkillToAgent(ctx, auth, t, sk); err != nil {
				lastErr = err.Error()
			} else {
				synced++
			}
		}
		st := "ok"
		detail := fmt.Sprintf("已同步 %d / %d 个 Skill 到 Agent", synced, len(skills))
		if lastErr != "" && synced == 0 {
			st = "error"
			detail = lastErr
		} else if lastErr != "" {
			detail += "（部分失败：" + lastErr + "）"
		}
		results = append(results, skillSyncTargetResult{
			Cluster: t.Cluster, Instance: t.Instance, SyncedCount: synced, Status: st, Detail: detail,
		})
	}
	return results, nil
}

func (h *GatewayHub) syncOneSkillToAgent(ctx context.Context, auth TokenAuth, target monitorTargetRef, sk platformSkillRecord) error {
	callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 15*time.Second)
	defer cancel()
	_, err := h.RunInternalToolCall(callCtx, target.Cluster, target.Instance, skillSyncTool, map[string]any{
		"session_id": "skill-sync",
		"slug":       sk.Slug,
		"skill_md":   sk.SkillMD,
		"files":      sk.Files,
		"remove":     false,
	})
	return err
}
