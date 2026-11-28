package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// 默认排除的目录/文件
var defaultExcludes = []string{
	".git",
	".doops",
	".agent",
	".claude",
	".local",
	".pnpm-store",
	".npm",
	".cache",
	"node_modules",
	"vendor",
	"__pycache__",
	".idea",
	".vscode",
	".turbo",
	".next",
	".nuxt",
	".svelte-kit",
	"coverage",
	"tmp",
	"temp",
	".DS_Store",
}

func loadIgnorePatterns(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 %s 失败: %v", filepath.Base(path), err)
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %v", filepath.Base(path), err)
	}
	return dedupePatterns(patterns), nil
}

func dedupePatterns(patterns []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		result = append(result, pattern)
	}
	return result
}

func gitSubmoduleExcludes(src string) ([]string, error) {
	repoRoot, repoPrefix, ok := detectGitRepo(src)
	if !ok {
		return nil, nil
	}

	cmd := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--stage")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("读取 Git submodule 路径失败: %v", err)
	}

	var excludes []string
	for _, record := range strings.Split(string(out), "\x00") {
		if record == "" {
			continue
		}
		metaAndPath := strings.SplitN(record, "\t", 2)
		if len(metaAndPath) != 2 {
			continue
		}
		fields := strings.Fields(metaAndPath[0])
		if len(fields) == 0 || fields[0] != "160000" {
			continue
		}
		rel, ok := repoPathWithinPrefix(filepath.ToSlash(metaAndPath[1]), repoPrefix)
		if !ok || rel == "" || rel == "." {
			continue
		}
		excludes = append(excludes, rel+"/")
	}
	return dedupePatterns(excludes), nil
}

func repoPathWithinPrefix(repoPath, repoPrefix string) (string, bool) {
	repoPath = path.Clean(filepath.ToSlash(strings.TrimSpace(repoPath)))
	repoPrefix = path.Clean(filepath.ToSlash(strings.TrimSpace(repoPrefix)))
	if repoPath == "." {
		return "", false
	}
	if repoPrefix == "." || repoPrefix == "" {
		return repoPath, true
	}
	if repoPath == repoPrefix {
		return ".", true
	}
	prefix := repoPrefix + "/"
	if !strings.HasPrefix(repoPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(repoPath, prefix), true
}

func filterGitIgnored(src string, relPaths []string) ([]string, error) {
	if len(relPaths) == 0 {
		return relPaths, nil
	}

	repoRoot, repoPrefix, ok := detectGitRepo(src)
	gitDir := ""
	if !ok {
		var err error
		gitDir, err = os.MkdirTemp("", "doops-ignore-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(gitDir)
		cmd := exec.Command("git", "init", "--bare", "--quiet", gitDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("initialize snapshot ignore context: %w: %s", err, strings.TrimSpace(string(out)))
		}
		repoRoot, repoPrefix = resolvePath(src), ""
	}

	repoPaths := make([]string, 0, len(relPaths))
	relByRepoPath := make(map[string]string, len(relPaths))
	for _, rel := range relPaths {
		repoPath := filepath.ToSlash(rel)
		if repoPrefix != "" {
			repoPath = path.Join(repoPrefix, repoPath)
		}
		repoPaths = append(repoPaths, repoPath)
		relByRepoPath[repoPath] = rel
	}

	ignored, err := gitIgnoredPaths(repoRoot, repoPaths, gitDir)
	if err != nil {
		return nil, err
	}

	filtered := make([]string, 0, len(relPaths))
	for _, repoPath := range repoPaths {
		if ignored[repoPath] {
			continue
		}
		filtered = append(filtered, relByRepoPath[repoPath])
	}
	return filtered, nil
}

func detectGitRepo(src string) (repoRoot string, repoPrefix string, ok bool) {
	cmd := exec.Command("git", "-C", src, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", "", false
	}

	repoRoot = strings.TrimSpace(string(out))
	cleanSrc := strings.TrimRight(src, string(os.PathSeparator))
	// Resolve symlinks on both sides so macOS /tmp vs /private/tmp (and similar
	// alias paths) do not produce a "../../../tmp/..." prefix that breaks
	// git check-ignore --stdin.
	repoRoot = resolvePath(repoRoot)
	cleanSrc = resolvePath(cleanSrc)
	repoPrefix, err = filepath.Rel(repoRoot, cleanSrc)
	if err != nil {
		return "", "", false
	}
	if repoPrefix == "." {
		repoPrefix = ""
	}
	if strings.HasPrefix(repoPrefix, "..") {
		// Source is not inside the detected repo after normalization; skip
		// git-ignore filtering rather than feeding invalid paths to git.
		return "", "", false
	}
	repoPrefix = filepath.ToSlash(repoPrefix)
	return repoRoot, repoPrefix, true
}

func resolvePath(path string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" {
		return clean
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved
	}
	return clean
}

func gitIgnoredPaths(repoRoot string, repoPaths []string, gitDir string) (map[string]bool, error) {
	if len(repoPaths) == 0 {
		return map[string]bool{}, nil
	}

	args := []string{"-C", repoRoot}
	if gitDir != "" {
		args = append(args, "--git-dir="+gitDir, "--work-tree="+repoRoot)
	}
	args = append(args, "check-ignore", "--stdin", "--verbose", "--non-matching", "-z")
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(strings.Join(repoPaths, "\x00") + "\x00")

	out, err := cmd.CombinedOutput()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 1 {
			return nil, fmt.Errorf("git check-ignore 失败: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}

	ignored := make(map[string]bool, len(repoPaths))
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(out) == 0 {
		return ignored, nil
	}
	if len(fields)%4 != 0 {
		return nil, fmt.Errorf("invalid git check-ignore response")
	}
	for index := 0; index < len(fields); index += 4 {
		pattern, repoPath := fields[index+2], fields[index+3]
		ignored[filepath.ToSlash(repoPath)] = pattern != "" && !strings.HasPrefix(pattern, "!")
	}

	return ignored, nil
}

func shouldExcludePath(relPath string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}

	normalized := filepath.ToSlash(relPath)
	for _, pattern := range patterns {
		if matchIgnorePattern(normalized, pattern) {
			return true
		}
	}
	return false
}

func matchIgnorePattern(relPath, pattern string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}

	anchored := strings.HasPrefix(pattern, "/")
	dirOnly := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "./")
	pattern = strings.TrimPrefix(pattern, "/")
	if pattern == "" {
		return false
	}

	hasMeta := strings.ContainsAny(pattern, "*?[")
	if strings.Contains(pattern, "/") {
		if matched, _ := path.Match(pattern, relPath); matched {
			return true
		}
		if !hasMeta && (relPath == pattern || strings.HasPrefix(relPath, pattern+"/")) {
			return true
		}
	}
	if anchored {
		if matched, _ := path.Match(pattern, relPath); matched {
			return true
		}
		return !hasMeta && (relPath == pattern || strings.HasPrefix(relPath, pattern+"/"))
	}

	base := path.Base(relPath)
	if matched, _ := path.Match(pattern, base); matched {
		return true
	}

	if !hasMeta {
		for _, segment := range strings.Split(relPath, "/") {
			if segment == pattern {
				return true
			}
		}
		if dirOnly && strings.HasPrefix(relPath, pattern+"/") {
			return true
		}
	}

	return false
}
