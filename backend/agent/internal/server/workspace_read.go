package server

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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
	switch strings.ToLower(strings.TrimSpace(args.Operation)) {
	case "list":
		return workspaceReadList(root, args.Path)
	case "read":
		return workspaceReadFile(args.SessionID, args.Path)
	case "changes":
		return workspaceReadChanges(root)
	case "diff":
		return workspaceReadDiff(root, args.Path)
	case "search":
		return workspaceReadSearch(root, args.Query)
	case "git-summary":
		return workspaceReadGitSummary(root)
	case "git-log":
		return workspaceReadGitLog(root, args.Ref)
	default:
		return "", fmt.Errorf("unsupported workspace read operation: %s", args.Operation)
	}
}

func workspaceReadList(root, rawDir string) (string, error) {
	dir, err := workspaceReadPath(root, rawDir, true)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	result := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return "", infoErr
		}
		relative, relErr := filepath.Rel(root, filepath.Join(dir, entry.Name()))
		if relErr != nil {
			return "", relErr
		}
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

func workspaceReadFile(sessionID, path string) (string, error) {
	result, err := handleFileRead(mustWorkspaceFileReadArgs(sessionID, path))
	if err != nil {
		return "", err
	}
	return workspaceReadJSON(map[string]interface{}{
		"path": path, "content": result, "encoding": "utf-8", "contentType": workspaceContentType(path),
		"bytes": len(result), "truncated": false, "binary": false,
	})
}

func mustWorkspaceFileReadArgs(sessionID, path string) json.RawMessage {
	data, _ := json.Marshal(api.FileReadParams{SessionID: sessionID, Path: path})
	return data
}

func workspaceReadChanges(root string) (string, error) {
	if !workspaceIsGit(root) {
		return workspaceReadJSON(map[string]interface{}{"gitStatus": "no_repository", "gitAvailable": false, "changes": []workspaceReadChange{}})
	}
	out, err := workspaceGitOutput(root, "status", "--short", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	changes := make([]workspaceReadChange, 0)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		code, raw := line[:2], strings.TrimSpace(line[3:])
		if raw == "" {
			continue
		}
		change := workspaceReadChange{Name: filepath.Base(raw), Path: filepath.ToSlash(raw), Status: "modified"}
		if strings.Contains(code, "?") || strings.Contains(code, "A") {
			change.Status = "created"
		} else if strings.Contains(code, "D") {
			change.Status = "deleted"
		} else if strings.Contains(code, "R") && strings.Contains(raw, " -> ") {
			parts := strings.SplitN(raw, " -> ", 2)
			change.Status, change.OldPath, change.NewPath, change.Path, change.Name = "renamed", parts[0], parts[1], parts[1], filepath.Base(parts[1])
		}
		changes = append(changes, change)
	}
	return workspaceReadJSON(map[string]interface{}{"gitStatus": "ready", "gitAvailable": true, "changes": changes})
}

func workspaceReadDiff(root, rawPath string) (string, error) {
	path, err := workspaceReadRelativePath(rawPath)
	if err != nil {
		return "", err
	}
	if !workspaceIsGit(root) {
		return workspaceReadJSON(map[string]interface{}{"path": path, "baseline": nil, "gitStatus": "no_repository", "stale": false,
			"before": map[string]interface{}{"present": false, "content": "", "encoding": "utf-8", "truncated": false},
			"after":  map[string]interface{}{"present": false, "content": "", "encoding": "utf-8", "truncated": false}})
	}
	before, _ := workspaceGitOutput(root, "show", "HEAD:"+path)
	after, afterErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	head, _ := workspaceGitOutput(root, "rev-parse", "HEAD")
	return workspaceReadJSON(map[string]interface{}{"path": path, "baseline": strings.TrimSpace(head), "gitStatus": "ready", "stale": false,
		"before": map[string]interface{}{"present": before != "", "content": before, "encoding": "utf-8", "truncated": false},
		"after":  map[string]interface{}{"present": afterErr == nil, "content": string(after), "encoding": "utf-8", "truncated": false}})
}

func workspaceReadSearch(root, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return workspaceReadJSON(map[string]interface{}{"entries": []interface{}{}, "truncated": false})
	}
	cmd := exec.Command("grep", "-RFil", "--exclude-dir=.git", "--exclude-dir=node_modules", "--", query, root)
	output, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return workspaceReadJSON(map[string]interface{}{"entries": []interface{}{}, "truncated": false})
		}
		return "", err
	}
	entries := make([]map[string]interface{}, 0)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		relative, relErr := filepath.Rel(root, line)
		if relErr != nil || relative == "." || strings.HasPrefix(relative, "..") {
			continue
		}
		relative = filepath.ToSlash(relative)
		entries = append(entries, map[string]interface{}{"name": filepath.Base(relative), "path": relative, "type": "file"})
	}
	return workspaceReadJSON(map[string]interface{}{"entries": entries, "truncated": false})
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
	out, err := workspaceGitOutput(root, "log", "-n", "50", "--format=%H%x09%aI%x09%an%x09%s", "--", ref)
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

func workspaceReadPath(root, raw string, allowRoot bool) (string, error) {
	if strings.TrimSpace(raw) == "" && allowRoot {
		return root, nil
	}
	path, err := workspaceReadRelativePath(raw)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(path)), nil
}

func workspaceReadRelativePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "." || filepath.IsAbs(raw) || strings.Contains(raw, "\x00") {
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
	return exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree").Run() == nil
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
		return "text/plain"
	}
}
