package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultAgentSessionRetention keeps persisted doagent conversation histories
// (.agent/sessions/) longer than the 24-hour temporary workspace artifact GC,
// so historical chats remain readable and resumable on the Agent.
const defaultAgentSessionRetention = 30 * 24 * time.Hour

func agentSessionRetention(workspaceMaxAge time.Duration) time.Duration {
	if defaultAgentSessionRetention > workspaceMaxAge {
		return defaultAgentSessionRetention
	}
	return workspaceMaxAge
}

// StartWorkspaceGC 启动后台垃圾回收守护协程以清理过期会话区和裸仓库
// maxAge: 目录被保留的最长时间
// interval: 扫描间隔
func StartWorkspaceGC(maxAge time.Duration, interval time.Duration) {
	log.Printf("🧹 Workspace GC started: maxAge=%v, interval=%v", maxAge, interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 启动时先执行一次清理
	performCleanup(maxAge)

	for range ticker.C {
		performCleanup(maxAge)
	}
}

func performCleanup(maxAge time.Duration) {
	performCleanupAt(time.Now(), maxAge, agentSessionRetention(maxAge))
}

func performCleanupAt(now time.Time, maxAge, sessionRetention time.Duration) {
	// Trashed knowledge base documents are files, not session directories, so
	// they expire on the same schedule through their own sweep.
	pruneKnowledgeBaseTrash(maxAge)
	wsRoot := workspaceRoot()
	cleanDirs := []struct {
		path             string
		preserveSessions bool
	}{
		{path: wsRoot, preserveSessions: true},
		{path: filepath.Join(os.TempDir(), "repos"), preserveSessions: false},
	}

	for _, target := range cleanDirs {
		entries, err := os.ReadDir(target.path)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("⚠️ [GC] Failed to read %s: %v", target.path, err)
			}
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				// /tmp/repos 下的 bare repo 可能是目录;
				// 但如果里面混杂了文件，也应该跳过。
				continue
			}

			// 跳过一些被保留的全局目录
			if entry.Name() == ".global" {
				continue
			}

			dirPath := filepath.Join(target.path, entry.Name())
			info, err := entry.Info()
			if err != nil {
				log.Printf("⚠️ [GC] Failed to get info for %s: %v", dirPath, err)
				continue
			}

			sessionsDir := filepath.Join(dirPath, ".agent", "sessions")
			hasActiveSessions := false
			if target.preserveSessions {
				if sessionLatest, found := dirLatestModTime(sessionsDir); found {
					if now.Sub(sessionLatest) <= sessionRetention {
						hasActiveSessions = true
					}
				}
			}

			// 深度遍历目录，获取非会话文件的最新修改时间，以防父目录 ModTime 未改变导致误删
			latestModTime := info.ModTime()
			hasNonSessionEntries := false
			_ = filepath.Walk(dirPath, func(path string, f os.FileInfo, err error) error {
				if err != nil {
					return nil // 忽略无权限或已删除的文件
				}
				if target.preserveSessions && (path == sessionsDir || strings.HasPrefix(path, sessionsDir+string(filepath.Separator))) {
					if f.IsDir() && path == sessionsDir {
						return filepath.SkipDir
					}
					return nil
				}
				if path != dirPath && path != filepath.Join(dirPath, ".agent") {
					hasNonSessionEntries = true
				}
				if f.ModTime().After(latestModTime) {
					latestModTime = f.ModTime()
				}
				return nil
			})

			age := now.Sub(latestModTime)
			if age <= maxAge {
				continue
			}

			if hasActiveSessions {
				if !hasNonSessionEntries {
					continue
				}
				log.Printf("🗑️ [GC] Pruning expired workspace artifacts while preserving AI sessions: %s (age: %v)",
					dirPath, age.Round(time.Hour))
				workspaceReplacementMu.Lock()
				current, err := os.Lstat(dirPath)
				if err == nil && os.SameFile(info, current) {
					if err := cleanWorkspaceArtifactsPreservingSessions(dirPath); err != nil {
						log.Printf("💥 [GC] Failed to prune workspace artifacts in %s: %v", dirPath, err)
					}
				}
				workspaceReplacementMu.Unlock()
				continue
			}

			log.Printf("🗑️ [GC] Removing expired workspace/repo item: %s (age: %v)",
				dirPath, age.Round(time.Hour))

			workspaceReplacementMu.Lock()
			current, err := os.Lstat(dirPath)
			if err == nil && os.SameFile(info, current) && current.ModTime().Equal(info.ModTime()) {
				if err := os.RemoveAll(dirPath); err != nil {
					log.Printf("💥 [GC] Failed to remove %s: %v", dirPath, err)
				}
			}
			workspaceReplacementMu.Unlock()
		}
	}
}

func dirLatestModTime(root string) (time.Time, bool) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return time.Time{}, false
	}
	latest := info.ModTime()
	foundFile := false
	_ = filepath.Walk(root, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if f.Mode().IsRegular() {
			foundFile = true
		}
		if f.ModTime().After(latest) {
			latest = f.ModTime()
		}
		return nil
	})
	return latest, foundFile
}

// cleanWorkspaceArtifactsPreservingSessions removes workspace files while
// keeping .agent/sessions intact so AI conversation history survives cleanup.
func cleanWorkspaceArtifactsPreservingSessions(wsPath string) error {
	entries, err := os.ReadDir(wsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		full := filepath.Join(wsPath, name)
		if name != ".agent" {
			if err := os.RemoveAll(full); err != nil {
				return err
			}
			continue
		}
		agentEntries, err := os.ReadDir(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, ae := range agentEntries {
			if ae.Name() == "sessions" {
				continue
			}
			if err := os.RemoveAll(filepath.Join(full, ae.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

type cleanWorkspaceParams struct {
	Workspace string `json:"workspace"`
}

func handleCleanWorkspace(argBytes json.RawMessage) (string, error) {
	var args cleanWorkspaceParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid clean workspace params: %w", err)
	}
	workspace := strings.TrimSpace(args.Workspace)
	if err := validateSession(workspace); err != nil {
		return "", err
	}
	wsPath := filepath.Join(workspaceRoot(), workspace)
	repoPath := filepath.Join("/tmp/repos", workspace+".git")
	workspaceReplacementMu.Lock()
	defer workspaceReplacementMu.Unlock()
	if _, hasSessions := dirLatestModTime(filepath.Join(wsPath, ".agent", "sessions")); hasSessions {
		if err := cleanWorkspaceArtifactsPreservingSessions(wsPath); err != nil {
			return "", err
		}
	} else if err := os.RemoveAll(wsPath); err != nil {
		return "", err
	}
	if err := os.RemoveAll(repoPath); err != nil {
		return "", err
	}
	return fmt.Sprintf("Cleaned workspace and bare repo: %s", workspace), nil
}
