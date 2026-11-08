package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Pull syncs a remote session workspace back to a local directory using the
// same Git transport model as Push. read remains a small-text viewing command.
func Pull(server Server, dest, sessionID string) error {
	return pullWorkspace(server, dest, sessionID, "gateway", "", "", "")
}

func pullWorkspace(server Server, dest, sessionID, storage, credential, token, commit string) error {
	mode, err := normalizeStorageMode(storage)
	if err != nil {
		return err
	}
	if mode == "cos" {
		return pullCOS(server, dest, sessionID, credential, token, commit, nil)
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session ID 必传：请通过 -session 指定要拉取的远端工作区")
	}
	if strings.TrimSpace(dest) == "" {
		dest = sessionID
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("解析目标路径失败: %v", err)
	}

	token, err = ResolveToken(server)
	if err != nil {
		return err
	}
	gitURL, err := buildGitRemoteURLForServer(server, sessionID, true)
	if err != nil {
		return err
	}

	fmt.Printf("📦 正在让远端工作区生成 Git 快照: /root/ws/%s\n", sessionID)
	client := NewMCPClient(server, NewSessionStore(), sessionID, false)
	client.Token = token
	output, err := client.CallAndCapture("doops_shell", map[string]interface{}{
		"command": buildGitWorkspaceCommitCommand(sessionID),
	})
	client.Close()
	if err != nil {
		return fmt.Errorf("远端工作区生成 Git 快照失败: %v", err)
	}
	commit, err = pullSnapshotCommit(output)
	if err != nil {
		return err
	}
	fmt.Printf("✅ 远端快照已生成: %s\n", commit)

	if err := pullGitWorkspace(gitURL, token, absDest, commit); err != nil {
		return err
	}
	fmt.Printf("✅ 已拉取远端工作区到本地: %s\n", absDest)
	return nil
}

func buildGitWorkspaceCommitCommand(sessionID string) string {
	wsPath := "/root/ws/" + sessionID
	repoPath := "/tmp/repos/" + sessionID + ".git"
	git := "git --git-dir " + shellQuote(repoPath) + " --work-tree " + shellQuote(wsPath)
	return strings.Join([]string{
		"set -e",
		"test -d " + shellQuote(wsPath),
		"mkdir -p /tmp/repos",
		"if [ ! -d " + shellQuote(repoPath) + " ]; then git init --bare " + shellQuote(repoPath) + " >/dev/null; fi",
		"cd " + shellQuote(wsPath),
		// A private index leaves the source .git and the deployment index untouched.
		"snapshot=$(mktemp -d)",
		"trap 'rm -rf \"$snapshot\"' EXIT",
		"export GIT_INDEX_FILE=\"$snapshot/index\"",
		git + " read-tree --empty",
		// Enumerate files explicitly so nested repositories cannot become gitlinks.
		"find . -name .git -prune -o \\( -type f -o -type l \\) -print0 > \"$snapshot/paths\"",
		"while IFS= read -r -d '' path; do printf '%s\\0' \"${path#./}\"; done < \"$snapshot/paths\" > \"$snapshot/files\"",
		"LC_ALL=C sort -z -o \"$snapshot/files\" \"$snapshot/files\"",
		git + " update-index --add -z --stdin < \"$snapshot/files\"",
		"tree=$(" + git + " write-tree)",
		git + " ls-tree -r -z --name-only \"$tree\" > \"$snapshot/tree-files\"",
		"LC_ALL=C sort -z -o \"$snapshot/tree-files\" \"$snapshot/tree-files\"",
		"cmp \"$snapshot/files\" \"$snapshot/tree-files\"",
		"commit=$(" + git + " -c user.name=doops -c user.email=doops@localhost -c commit.gpgsign=false commit-tree \"$tree\" -m DoopsPullSnapshot)",
		// Updating a download ref does not run receive-pack's deployment hook.
		git + " update-ref refs/heads/doops-pull \"$commit\"",
		"printf 'pull-commit:%s\\n' \"$commit\"",
	}, "\n")
}

func pullSnapshotCommit(output string) (string, error) {
	var commit string
	for _, line := range strings.Split(output, "\n") {
		value, found := strings.CutPrefix(line, "pull-commit:")
		if !found {
			continue
		}
		if commit != "" || !validWorkspaceCommit(value) {
			return "", fmt.Errorf("invalid pull snapshot commit response")
		}
		commit = strings.TrimSpace(value)
	}
	if commit == "" {
		return "", fmt.Errorf("remote workspace returned no pull snapshot commit")
	}
	return commit, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func pullGitWorkspace(gitURL, token, dest, commit string) error {
	if !validWorkspaceCommit(commit) {
		return fmt.Errorf("invalid pull snapshot commit")
	}
	gitEnv, cleanupGitAuth, err := prepareGitAuthEnvironment(token)
	if err != nil {
		return err
	}
	defer cleanupGitAuth()

	existingRepo := false
	if info, err := os.Stat(dest); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("目标路径已存在且不是目录: %s", dest)
		}
		if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
			existingRepo = true
		} else {
			entries, err := os.ReadDir(dest)
			if err != nil {
				return fmt.Errorf("读取目标目录失败: %v", err)
			}
			if len(entries) > 0 {
				return fmt.Errorf("目标目录已存在且非空，也不是 Git 仓库: %s", dest)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查目标路径失败: %v", err)
	}

	if !existingRepo {
		if err := runGit(gitEnv, "", "init", "-b", "master", dest); err != nil {
			return err
		}
	}
	fmt.Printf("⬇️  正在拉取远端工作区快照 %s 到: %s\n", commit, dest)
	if err := runGit(gitEnv, dest, "fetch", "--no-tags", gitURL, commit); err != nil {
		return err
	}
	return runGit(gitEnv, dest, "reset", "--hard", commit)
}

func runGit(env []string, dir string, args ...string) error {
	gitArgs := append([]string{"-c", "credential.helper="}, args...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Env = env
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := runIsolatedGitCommand(cmd); err != nil {
		return fmt.Errorf("git %v failed: %v", redactGitArgs(args), err)
	}
	return nil
}
