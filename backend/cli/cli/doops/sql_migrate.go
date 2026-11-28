package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type cliMigrationFile struct {
	Version     int64  `json:"version"`
	VersionTag  string `json:"version_tag"`
	Description string `json:"description"`
	EngineHint  string `json:"engine_hint"`
	FileName    string `json:"file_name"`
	Checksum    string `json:"checksum"`
	Content     string `json:"content,omitempty"`
}

var cliMigrationRe = regexp.MustCompile(`^V(\d+)__([A-Za-z0-9._-]+?)\.(sql|mongo\.json)$`)

// loadCLIMigrationDirJSON reads V00N__desc.sql / .mongo.json files from dir,
// verifies dosql.sum if present, and returns the serialized JSON manifest.
func loadCLIMigrationDirJSON(dir string, includeContent bool) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("读取迁移目录 %s: %w", dir, err)
	}
	var files []cliMigrationFile
	seen := map[int64]string{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "dosql.sum" {
			continue
		}
		m := cliMigrationRe.FindStringSubmatch(entry.Name())
		if len(m) != 4 {
			continue
		}
		ver, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || ver <= 0 {
			return "", fmt.Errorf("无效的迁移版本号: %s", entry.Name())
		}
		if prev, dup := seen[ver]; dup {
			return "", fmt.Errorf("迁移版本号重复: %s 与 %s (V%d)", prev, entry.Name(), ver)
		}
		seen[ver] = entry.Name()
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", err
		}
		hint := "sql"
		if m[3] == "mongo.json" {
			hint = "mongodb"
		}
		sum := sha256.Sum256(raw)
		f := cliMigrationFile{
			Version:     ver,
			VersionTag:  "V" + m[1],
			Description: m[2],
			EngineHint:  hint,
			FileName:    entry.Name(),
			Checksum:    "sha256:" + hex.EncodeToString(sum[:]),
		}
		if includeContent {
			f.Content = string(raw)
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Version < files[j].Version })
	if err := verifyCLISumFile(dir, files); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(files)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func verifyCLISumFile(dir string, files []cliMigrationFile) error {
	raw, err := os.ReadFile(filepath.Join(dir, "dosql.sum"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var b strings.Builder
	h := sha256.New()
	for _, f := range files {
		line := fmt.Sprintf("%s %s\n", f.FileName, f.Checksum)
		h.Write([]byte(line))
		b.WriteString(line)
	}
	want := fmt.Sprintf("h1:%s\n%s", hex.EncodeToString(h.Sum(nil)), b.String())
	if strings.TrimSpace(string(raw)) != strings.TrimSpace(want) {
		return errors.New("dosql_migrate_sum_mismatch: 迁移目录与 dosql.sum 不一致，请核对文件完整性")
	}
	return nil
}

type cliHistoryRecord struct {
	Mode      string `json:"mode"`
	Kind      string `json:"kind"`
	Statement string `json:"statement"`
	StmtHash  string `json:"stmt_hash"`
	Status    string `json:"status"`
}

func isPackableCLIRecord(r cliHistoryRecord) bool {
	if r.Status != "succeeded" {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(r.Kind))
	if kind == "read" || kind == "backfill" {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(r.Mode))
	if mode != "" && mode != "apply" {
		return false
	}
	return true
}

func writeCLIFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-dosql-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// packCLIHistoryToDir writes succeeded mutating statements from a DoSql history
// JSON payload into the next V00N__<desc>.sql / .mongo.json file and updates dosql.sum.
func packCLIHistoryToDir(dir, description, engineHint string, historyJSON []byte) (string, int, error) {
	var payload struct {
		Engine  string             `json:"engine"`
		Records []cliHistoryRecord `json:"records"`
	}
	if err := json.Unmarshal(historyJSON, &payload); err != nil {
		return "", 0, fmt.Errorf("解析 history JSON 失败: %w", err)
	}
	engine := strings.TrimSpace(engineHint)
	if engine == "" {
		engine = payload.Engine
	}
	slug := sanitizeCLISlug(description)
	if slug == "" {
		return "", 0, errors.New("--mode pack 需要有效的 --desc <slug>")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", 0, err
	}
	existingJSON, err := loadCLIMigrationDirJSON(dir, false)
	if err != nil {
		return "", 0, err
	}
	var existing []cliMigrationFile
	_ = json.Unmarshal([]byte(existingJSON), &existing)
	var nextVer int64 = 1
	if len(existing) > 0 {
		nextVer = existing[len(existing)-1].Version + 1
	}

	var stmts []string
	lastHash := ""
	for _, r := range payload.Records {
		if !isPackableCLIRecord(r) {
			continue
		}
		s := strings.TrimSpace(r.Statement)
		if s == "" || (r.StmtHash != "" && r.StmtHash == lastHash) {
			continue
		}
		lastHash = r.StmtHash
		stmts = append(stmts, s)
	}
	if len(stmts) == 0 {
		return "", 0, errors.New("dosql_pack_no_mutations: 当前历史记录中没有可打包的成功变更语句")
	}

	ext := "sql"
	var content strings.Builder
	if strings.EqualFold(engine, "mongodb") {
		ext = "mongo.json"
		content.WriteString("[\n  ")
		content.WriteString(strings.Join(stmts, ",\n  "))
		content.WriteString("\n]\n")
	} else {
		content.WriteString(fmt.Sprintf("-- Packed by DoSql (version V%03d, %d statements)\n", nextVer, len(stmts)))
		for _, s := range stmts {
			if !strings.HasSuffix(s, ";") {
				s += ";"
			}
			content.WriteString(s + "\n")
		}
	}

	fileName := fmt.Sprintf("V%03d__%s.%s", nextVer, slug, ext)
	fullPath := filepath.Join(dir, fileName)
	raw := []byte(content.String())
	if err := writeCLIFileAtomic(fullPath, raw, 0644); err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(raw)
	existing = append(existing, cliMigrationFile{
		Version:  nextVer,
		FileName: fileName,
		Checksum: "sha256:" + hex.EncodeToString(sum[:]),
	})
	var sb strings.Builder
	h := sha256.New()
	for _, f := range existing {
		line := fmt.Sprintf("%s %s\n", f.FileName, f.Checksum)
		h.Write([]byte(line))
		sb.WriteString(line)
	}
	sumContent := fmt.Sprintf("h1:%s\n%s", hex.EncodeToString(h.Sum(nil)), sb.String())
	if err := writeCLIFileAtomic(filepath.Join(dir, "dosql.sum"), []byte(sumContent), 0644); err != nil {
		return "", 0, err
	}
	return fullPath, len(stmts), nil
}

func sanitizeCLISlug(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_-")
}

const (
	defaultDoSQLDirName       = ".dosql"
	defaultDoSQLPolicyRelPath = ".dosql/policy.yaml"
	defaultDoSQLMigrationsRel = ".dosql/migrations"
)

const defaultDoSQLPolicyTemplate = `# DoSql Policy-as-Code 规范（由 doops sql --mode init 生成）
# 严禁在本目录写入数据库密码、connection_uri 或 token；凭据由 DoOps Gateway 统一托管。
schema: dosql.policy.v1

mysql:
  require_primary_key: warn
  require_charset_utf8mb4: warn
  forbid_float_types: warn

postgres:
  require_concurrent_index: warn

dml:
  forbid_unbounded_update_delete: error
  forbid_truncate: warn

release:
  expand_contract_guard: warn
`

const defaultDoSQLReadmeTemplate = `# .dosql — DoSql 项目数据库治理目录

本目录随业务代码一起纳入 Git 版本管理，存放**不含任何凭据**的数据库治理与迁移资产：

- ` + "`policy.yaml`" + `：Policy-as-Code 规范门禁；` + "`doops sql`" + ` 未显式传 ` + "`--policy`" + ` 时会自动发现本文件。
- ` + "`migrations/`" + `：按版本编号的迁移脚本（` + "`V001__init.sql`" + ` / ` + "`V002__desc.mongo.json`" + `）及 ` + "`dosql.sum`" + ` 完整性清单；未传 ` + "`--dir`" + ` / ` + "`--out`" + ` 时自动发现本目录。多库项目可按子目录组织（如 ` + "`migrations/<db>/`" + `）并显式传 ` + "`--dir`" + `。
- ` + "`schema/`" + `：可选的期望/基线 SchemaIR 快照（用于代码评审与漂移基线）。

## 常用命令

` + "```bash" + `
# 规范检查（自动读取 .dosql/policy.yaml）
doops sql --target <t> --mode lint --file .dosql/migrations/V001__init.sql

# 将会话中自动记录的 SQL 变更打包为迁移文件（默认输出到 .dosql/migrations）
doops sql --target <t> --mode pack --database <db> --desc add_order_status

# 查看目标库迁移状态并执行幂等迁移（默认读取 .dosql/migrations）
doops sql --target <t> --mode status --database <db>
doops sql --target <t> --mode migrate --database <db> --release-tag v1.0.0
` + "```" + `

> 安全红线：严禁在 ` + "`.dosql/`" + ` 中保存数据库密码、DSN、` + "`connection_uri`" + ` 或 Token。所有数据库连接凭据均通过 ` + "`doops credential configure --use database`" + ` 由 Gateway 加密托管。
`

// initDoSQLProjectWorkspace scaffolds <baseDir>/.dosql/{policy.yaml,README.md,schema/,migrations/}
// idempotently without overwriting existing policy.yaml or README.md files.
func initDoSQLProjectWorkspace(baseDir string) (created []string, skipped []string, err error) {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		baseDir = "."
	}
	root := filepath.Join(baseDir, defaultDoSQLDirName)
	for _, dir := range []string{
		root,
		filepath.Join(root, "schema"),
		filepath.Join(root, "migrations"),
	} {
		if _, statErr := os.Stat(dir); errors.Is(statErr, os.ErrNotExist) {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, nil, fmt.Errorf("创建目录 %s: %w", dir, err)
			}
			created = append(created, dir)
		} else if statErr != nil {
			return nil, nil, fmt.Errorf("检查目录 %s: %w", dir, statErr)
		} else {
			skipped = append(skipped, dir)
		}
	}
	for _, f := range []struct {
		path    string
		content string
	}{
		{filepath.Join(root, "policy.yaml"), defaultDoSQLPolicyTemplate},
		{filepath.Join(root, "README.md"), defaultDoSQLReadmeTemplate},
	} {
		if _, statErr := os.Stat(f.path); errors.Is(statErr, os.ErrNotExist) {
			if err := os.WriteFile(f.path, []byte(f.content), 0644); err != nil {
				return nil, nil, fmt.Errorf("写入 %s: %w", f.path, err)
			}
			created = append(created, f.path)
		} else if statErr != nil {
			return nil, nil, fmt.Errorf("检查文件 %s: %w", f.path, statErr)
		} else {
			skipped = append(skipped, f.path)
		}
	}
	return created, skipped, nil
}

// resolveDefaultPolicyFile returns explicit if provided, otherwise auto-discovers
// ./.dosql/policy.yaml when present as a regular file.
func resolveDefaultPolicyFile(explicit string) string {
	if p := strings.TrimSpace(explicit); p != "" {
		return p
	}
	candidate := filepath.FromSlash(defaultDoSQLPolicyRelPath)
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
		return candidate
	}
	return ""
}

// resolveDefaultMigrationsDir returns explicit if provided, otherwise
// auto-discovers ./.dosql/migrations when present as a directory.
func resolveDefaultMigrationsDir(explicit string) string {
	if d := strings.TrimSpace(explicit); d != "" {
		return d
	}
	candidate := filepath.FromSlash(defaultDoSQLMigrationsRel)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}
