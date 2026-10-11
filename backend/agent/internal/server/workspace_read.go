package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/user/doops/agent/api"
)

type workspaceReadChange struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	OldPath string `json:"oldPath,omitempty"`
	NewPath string `json:"newPath,omitempty"`
}

func handleWorkspaceRead(argBytes json.RawMessage) (string, error) {
	var args api.WorkspaceReadParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid workspace read params: %w", err)
	}
	if err := validateSession(args.SessionID); err != nil {
		return "", err
	}
	root, err := workspacePath(args.SessionID)
	if err != nil {
		return "", err
	}
	base, err := os.OpenRoot(workspaceRoot())
	if err != nil {
		return "", err
	}
	defer base.Close()
	info, err := base.Lstat(args.SessionID)
	if os.IsNotExist(err) {
		switch args.Operation {
		case "list", "search":
			return workspaceReadJSON(map[string]interface{}{"entries": []interface{}{}, "truncated": false})
		case "changes":
			return workspaceReadJSON(map[string]interface{}{"gitStatus": "no_repository", "gitAvailable": false, "changes": []workspaceReadChange{}})
		case "git-summary":
			return workspaceReadJSON(map[string]interface{}{"branch": nil, "clean": true, "detached": false, "head": nil, "refs": []interface{}{}, "status": "no_repository"})
		case "git-log":
			return workspaceReadJSON(map[string]interface{}{"commits": []interface{}{}, "cursor": nil, "ref": args.Ref, "truncated": false})
		}
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("workspace session cannot be a symbolic link")
	}
	scoped, err := base.OpenRoot(args.SessionID)
	if err != nil {
		return "", err
	}
	defer scoped.Close()
	switch strings.ToLower(strings.TrimSpace(args.Operation)) {
	case "list":
		return workspaceReadList(scoped, args.Path)
	case "read":
		return workspaceReadFile(scoped, args.Path)
	case "changes":
		return workspaceReadChanges(root, scoped)
	case "diff":
		return workspaceReadDiff(root, scoped, args.Path)
	case "search":
		return workspaceReadSearch(scoped, args.Query)
	case "git-summary":
		return workspaceReadGitSummary(root)
	case "git-log":
		return workspaceReadGitLog(root, args.Ref)
	default:
		return "", fmt.Errorf("unsupported workspace read operation: %s", args.Operation)
	}
}

func workspaceReadList(root *os.Root, rawDir string) (string, error) {
	dir, err := workspaceReadRelativePath(rawDir)
	if rawDir == "" || rawDir == "." {
		dir, err = ".", nil
	}
	if err != nil {
		return "", err
	}
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return "", err
	}
	result := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return "", infoErr
		}
		relative := filepath.Join(dir, entry.Name())
		item := map[string]interface{}{
			"name": entry.Name(), "path": filepath.ToSlash(relative),
			"type": map[bool]string{true: "directory", false: "file"}[entry.IsDir()],
		}
		if !entry.IsDir() {
			item["bytes"] = info.Size()
		}
		if modified := info.ModTime().UnixMilli(); modified > 0 {
			item["modifiedAt"] = modified
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i]["type"] != result[j]["type"] {
			return result[i]["type"] == "directory"
		}
		return result[i]["path"].(string) < result[j]["path"].(string)
	})
	return workspaceReadJSON(map[string]interface{}{"entries": result})
}

func workspaceReadFile(root *os.Root, path string) (string, error) {
	data, err := workspaceReadBytes(root, path)
	if err != nil {
		return "", err
	}
	content, encoding, binary := workspaceFileContent(data)
	return workspaceReadJSON(map[string]interface{}{
		"path": path, "content": content, "encoding": encoding, "contentType": workspaceContentType(path),
		"bytes": len(data), "truncated": false, "binary": binary,
	})
}

func workspaceReadChanges(root string, scoped *os.Root) (string, error) {
	if !workspaceIsGit(root) {
		changes := make([]workspaceReadChange, 0)
		err := fs.WalkDir(scoped.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
				return fs.SkipDir
			}
			if entry.Type().IsRegular() {
				changes = append(changes, workspaceReadChange{Name: entry.Name(), Path: path, Status: "created"})
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		return workspaceReadJSON(map[string]interface{}{"gitStatus": "no_repository", "gitAvailable": false, "changes": changes})
	}
	out, err := workspaceGitOutput(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	changes := make([]workspaceReadChange, 0)
	lines := strings.Split(out, "\x00")
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		if len(line) < 4 {
			continue
		}
		code, raw := line[:2], line[3:]
		if raw == "" {
			continue
		}
		change := workspaceReadChange{Name: filepath.Base(raw), Path: filepath.ToSlash(raw), Status: "modified"}
		if strings.Contains(code, "?") || strings.Contains(code, "A") {
			change.Status = "created"
		} else if strings.Contains(code, "D") {
			change.Status = "deleted"
		} else if strings.Contains(code, "R") && index+1 < len(lines) {
			index++
			change.Status, change.OldPath, change.NewPath = "renamed", lines[index], raw
		}
		changes = append(changes, change)
	}
	return workspaceReadJSON(map[string]interface{}{"gitStatus": "ready", "gitAvailable": true, "changes": changes})
}

func workspaceReadDiff(root string, scoped *os.Root, rawPath string) (string, error) {
	path, err := workspaceReadRelativePath(rawPath)
	if err != nil {
		return "", err
	}
	if !workspaceIsGit(root) {
		return workspaceReadJSON(map[string]interface{}{"path": path, "baseline": nil, "gitStatus": "no_repository", "stale": false,
			"before": map[string]interface{}{"present": false, "content": "", "encoding": "utf-8", "truncated": false},
			"after":  map[string]interface{}{"present": false, "content": "", "encoding": "utf-8", "truncated": false}})
	}
	before, beforeErr := workspaceGitOutput(root, "show", "HEAD:"+path)
	after, afterErr := workspaceReadBytes(scoped, path)
	if afterErr != nil && !os.IsNotExist(afterErr) {
		return "", afterErr
	}
	beforeContent, beforeEncoding, _ := workspaceFileContent([]byte(before))
	afterContent, afterEncoding, _ := workspaceFileContent(after)
	head, _ := workspaceGitOutput(root, "rev-parse", "HEAD")
	return workspaceReadJSON(map[string]interface{}{"path": path, "baseline": strings.TrimSpace(head), "gitStatus": "ready", "stale": false,
		"before": map[string]interface{}{"present": beforeErr == nil, "content": beforeContent, "encoding": beforeEncoding, "truncated": false},
		"after":  map[string]interface{}{"present": afterErr == nil, "content": afterContent, "encoding": afterEncoding, "truncated": false}})
}

func workspaceReadSearch(root *os.Root, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return workspaceReadJSON(map[string]interface{}{"entries": []interface{}{}, "truncated": false})
	}
	entries := make([]map[string]interface{}, 0)
	truncated := false
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			return fs.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxFileReadBytes() {
			truncated = true
			return nil
		}
		data, err := workspaceReadBytes(root, path)
		if err != nil {
			return err
		}
		if looksLikeText(data) && bytes.Contains(bytes.ToLower(data), []byte(strings.ToLower(query))) {
			entries = append(entries, map[string]interface{}{"name": entry.Name(), "path": path, "type": "file"})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return workspaceReadJSON(map[string]interface{}{"entries": entries, "truncated": truncated})
}

func workspaceReadGitSummary(root string) (string, error) {
	if !workspaceIsGit(root) {
		return workspaceReadJSON(map[string]interface{}{"branch": nil, "clean": true, "detached": false, "head": nil, "refs": []interface{}{}, "status": "no_repository"})
	}
	branch, _ := workspaceGitOutput(root, "branch", "--show-current")
	head, _ := workspaceGitOutput(root, "rev-parse", "HEAD")
	status, _ := workspaceGitOutput(root, "status", "--porcelain")
	branch, head = strings.TrimSpace(branch), strings.TrimSpace(head)
	return workspaceReadJSON(map[string]interface{}{"branch": workspaceNullable(branch), "clean": strings.TrimSpace(status) == "", "detached": branch == "", "head": workspaceNullable(head), "refs": []interface{}{}, "status": "ready"})
}

func workspaceReadGitLog(root, ref string) (string, error) {
	if !workspaceIsGit(root) {
		return workspaceReadJSON(map[string]interface{}{"commits": []interface{}{}, "cursor": nil, "ref": ref, "truncated": false})
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	commit, err := workspaceGitOutput(root, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	out, err := workspaceGitOutput(root, "log", "-n", "50", "--format=%H%x09%aI%x09%an%x09%s", strings.TrimSpace(commit), "--")
	if err != nil {
		return "", err
	}
	commits := make([]map[string]interface{}, 0)
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 4)
		if len(parts) == 4 {
			commits = append(commits, map[string]interface{}{"oid": parts[0], "authoredAt": parts[1], "author": parts[2], "subject": parts[3], "parents": []string{}})
		}
	}
	return workspaceReadJSON(map[string]interface{}{"commits": commits, "cursor": nil, "ref": ref, "truncated": false})
}

func workspaceReadBytes(root *os.Root, raw string) ([]byte, error) {
	path, err := workspaceReadRelativePath(raw)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileReadBytes() {
		return nil, fmt.Errorf("workspace preview requires a regular file no larger than %d bytes", maxFileReadBytes())
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileReadBytes()+1))
	if err == nil && int64(len(data)) > maxFileReadBytes() {
		err = fmt.Errorf("workspace preview exceeds %d bytes", maxFileReadBytes())
	}
	return data, err
}

func workspaceFileContent(data []byte) (string, string, bool) {
	if !looksLikeText(data) || !utf8.Valid(data) {
		return base64.StdEncoding.EncodeToString(data), "base64", true
	}
	return string(data), "utf-8", false
}

func workspaceReadRelativePath(raw string) (string, error) {
	if raw == "" || raw == "." || raw != strings.TrimSpace(raw) || filepath.IsAbs(raw) || strings.Contains(raw, "\x00") {
		return "", fmt.Errorf("workspace path is required and must be relative")
	}
	clean := filepath.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe workspace path: %q", raw)
	}
	return filepath.ToSlash(clean), nil
}

func workspaceGitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func workspaceIsGit(root string) bool {
	top, err := workspaceGitOutput(root, "rev-parse", "--show-toplevel")
	resolved, pathErr := filepath.EvalSymlinks(root)
	return err == nil && pathErr == nil && filepath.Clean(strings.TrimSpace(top)) == filepath.Clean(resolved)
}

func workspaceReadJSON(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func workspaceNullable(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func workspaceContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".js", ".ts", ".tsx":
		return "text/typescript"
	default:
		if mediaType := mime.TypeByExtension(filepath.Ext(path)); mediaType != "" {
			return mediaType
		}
		return "text/plain"
	}
}
