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
	now := time.Now()
	cleanDirs := []string{
		workspaceRoot(),
		filepath.Join(os.TempDir(), "repos"),
	}

	for _, baseDir := range cleanDirs {
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("⚠️ [GC] Failed to read %s: %v", baseDir, err)
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

			dirPath := filepath.Join(baseDir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				log.Printf("⚠️ [GC] Failed to get info for %s: %v", dirPath, err)
				continue
			}

			// 深度遍历目录，获取其内文件的最新修改时间，以防父目录 ModTime 未改变导致误删
			latestModTime := info.ModTime()
			_ = filepath.Walk(dirPath, func(path string, f os.FileInfo, err error) error {
				if err != nil {
					return nil // 忽略无权限或已删除的文件
				}
				if f.ModTime().After(latestModTime) {
					latestModTime = f.ModTime()
				}
				return nil
			})

			age := now.Sub(latestModTime)
			if age > maxAge {
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
	if err := os.RemoveAll(wsPath); err != nil {
		return "", err
	}
	if err := os.RemoveAll(repoPath); err != nil {
		return "", err
	}
	return fmt.Sprintf("Cleaned workspace and bare repo: %s", workspace), nil
}
