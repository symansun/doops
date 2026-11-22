package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const workspaceSourceManifestPath = ".doops/source.json"

type workspaceSourceManifest struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Source     WorkspaceSourceIdentity `json:"source"`
}

var immutableGitCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func createWorkspaceSnapshotDirectory(sessionID string) (string, error) {
	ensureSignalCleanupHandler()
	gitAuthCleanupMu.Lock()
	defer gitAuthCleanupMu.Unlock()
	if gitShutdownRequested {
		return "", fmt.Errorf("workspace snapshot canceled by shutdown")
	}
	dir, err := os.MkdirTemp("", "doops-sync-"+sessionID+"-")
	if err != nil {
		return "", err
	}
	signalCleanupDirs[dir] = struct{}{}
	return dir, nil
}

func validWorkspaceCommit(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func createWorkspaceSnapshotCommit(root string, source *WorkspaceSourceIdentity) (string, error) {
	gitCmds := [][]string{
		{"init", "-b", "master"},
		{"add", "--force", "."},
	}
	if source != nil {
		gitCmds = append(gitCmds, []string{"add", "-f", "--", workspaceSourceManifestPath})
	}
	gitCmds = append(gitCmds,
		// Internal snapshot commit: never use the host's signing setup.
		[]string{"-c", "user.name=doops", "-c", "user.email=doops@localhost", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "AutoSync"},
	)

	for _, args := range gitCmds {
		if label := describeGitStep(args); label != "" {
			fmt.Printf("⏳ %s...\n", label)
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("创建幽灵提交失败 (%v): %v: %s", args, err, strings.TrimSpace(string(output)))
		}
	}

	commit, err := gitCommandOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("读取本地幽灵提交失败: %v", err)
	}
	commit = strings.TrimSpace(commit)
	if source != nil {
		if err := validateWorkspaceSourceManifestInCommit(root, commit, *source); err != nil {
			return "", err
		}
	}
	return commit, nil
}

func validateWorkspaceSourceManifestInCommit(root, commit string, source WorkspaceSourceIdentity) error {
	object := commit + ":" + workspaceSourceManifestPath
	objectType, err := gitCommandOutput(root, "cat-file", "-t", object)
	if err != nil {
		return fmt.Errorf("transport commit is missing %s: %w", workspaceSourceManifestPath, err)
	}
	if strings.TrimSpace(objectType) != "blob" {
		return fmt.Errorf("transport commit %s must be a regular file", workspaceSourceManifestPath)
	}
	data, err := gitCommandOutput(root, "show", object)
	if err != nil {
		return fmt.Errorf("read transport source manifest: %w", err)
	}
	var manifest workspaceSourceManifest
	if err := json.Unmarshal([]byte(data), &manifest); err != nil {
		return fmt.Errorf("decode transport source manifest: %w", err)
	}
	if manifest.APIVersion != workspaceManifestAPIVersion || manifest.Kind != "WorkspaceSource" {
		return fmt.Errorf("transport source manifest has invalid type")
	}
	if manifest.Source != source {
		return fmt.Errorf("transport source manifest does not match declared source identity")
	}
	return nil
}

func workspaceSourceManifestBlobID(root, commit string) (string, error) {
	blob, err := gitCommandOutput(root, "rev-parse", commit+":"+workspaceSourceManifestPath)
	if err != nil {
		return "", fmt.Errorf("read transport source manifest blob: %w", err)
	}
	blob = strings.TrimSpace(blob)
	if !validWorkspaceCommit(blob) {
		return "", fmt.Errorf("transport source manifest blob ID is invalid")
	}
	return blob, nil
}

func stageWorkspaceSnapshot(
	src, tmpDir string,
	extraExcludes []string,
	dryRun bool,
	source *WorkspaceSourceIdentity,
) ([]string, error) {
	stagedFiles, err := stageSnapshot(src, tmpDir, extraExcludes, dryRun)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return stagedFiles, nil
	}
	if dryRun {
		stagedFiles = append(stagedFiles, workspaceSourceManifestPath)
	} else {
		manifestPath, err := writeWorkspaceSourceManifest(tmpDir, source)
		if err != nil {
			return nil, fmt.Errorf("write source identity manifest: %w", err)
		}
		stagedFiles = append(stagedFiles, manifestPath)
	}
	sort.Strings(stagedFiles)
	return stagedFiles, nil
}

func writeWorkspaceSourceManifest(root string, source *WorkspaceSourceIdentity) (string, error) {
	if source == nil {
		return "", fmt.Errorf("source identity is required")
	}
	if strings.TrimSpace(source.Repository) == "" {
		return "", fmt.Errorf("source repository is required")
	}
	if !immutableGitCommitPattern.MatchString(strings.TrimSpace(source.Revision)) {
		return "", fmt.Errorf("source revision must be an immutable Git commit")
	}
	if strings.TrimSpace(source.Branch) == "" || strings.TrimSpace(source.Branch) != source.Branch {
		return "", fmt.Errorf("source branch is required")
	}
	manifest := workspaceSourceManifest{
		APIVersion: workspaceManifestAPIVersion,
		Kind:       "WorkspaceSource",
		Source:     *source,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode source manifest: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(root, filepath.FromSlash(workspaceSourceManifestPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create source manifest directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write source manifest: %w", err)
	}
	return workspaceSourceManifestPath, nil
}

func stageSnapshot(src, tmpDir string, extraExcludes []string, dryRun bool) ([]string, error) {
	doopsPatterns, err := loadIgnorePatterns(filepath.Join(src, ".doopsignore"))
	if err != nil {
		return nil, err
	}
	allExcludes := dedupePatterns(append(append([]string{}, defaultExcludes...), extraExcludes...))
	submoduleExcludes, err := gitSubmoduleExcludes(src)
	if err != nil {
		return nil, err
	}
	allExcludes = dedupePatterns(append(allExcludes, submoduleExcludes...))
	candidates, err := walkSourceFiles(src, allExcludes)
	if err != nil {
		return nil, err
	}

	filtered := make([]string, 0, len(candidates))
	for _, rel := range candidates {
		if shouldExcludePath(rel, doopsPatterns) {
			continue
		}
		filtered = append(filtered, rel)
	}

	filtered, err = filterGitIgnored(src, filtered)
	if err != nil {
		return nil, err
	}
	sort.Strings(filtered)

	if dryRun {
		return filtered, nil
	}

	for _, rel := range filtered {
		srcPath := filepath.Join(src, rel)
		dstPath := filepath.Join(tmpDir, rel)
		if err := copySnapshotEntry(srcPath, dstPath); err != nil {
			return nil, fmt.Errorf("复制 %s 失败: %v", rel, err)
		}
	}

	return filtered, nil
}

func walkSourceFiles(root string, excludes []string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}

		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		rel = filepath.Clean(rel)

		if shouldExcludePath(rel, excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode.IsRegular() || mode&os.ModeSymlink != 0 {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func copySnapshotEntry(srcPath, dstPath string) error {
	info, err := os.Lstat(srcPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(srcPath)
		if err != nil {
			return err
		}
		return os.Symlink(target, dstPath)
	}

	if !info.Mode().IsRegular() {
		return nil
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		return err
	}

	if err := dstFile.Close(); err != nil {
		return err
	}
	return os.Chmod(dstPath, info.Mode().Perm())
}
