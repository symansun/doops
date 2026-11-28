package main

import (
	"fmt"
	"strings"
)

// resolveCleanWorkspace selects the workspace to clean. Explicit --workspace
// always takes precedence; otherwise an explicit -session is reused. When
// neither is provided, it fails closed so default or generated workspaces are
// never deleted accidentally.
func resolveCleanWorkspace(workspace, explicitSession string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace != "" {
		return workspace, nil
	}
	explicitSession = strings.TrimSpace(explicitSession)
	if explicitSession != "" {
		return explicitSession, nil
	}
	return "", fmt.Errorf("--workspace or explicit -session is required")
}

// Clean 通过 MCP doops_shell 清理远端的挂载工作区及残留裸点仓库
func Clean(client *MCPClient, workspace string) error {
	wsPath := "/root/ws/" + workspace
	repoPath := "/tmp/repos/" + workspace + ".git"

	fmt.Printf("🗑️  清理远端工作区与源仓库: %s, %s\n", wsPath, repoPath)

	// 安全防注入检查
	if workspace == "" || workspace == "/" || strings.Contains(workspace, "..") {
		return fmt.Errorf("不安全的 workspace 名称: %s", workspace)
	}

	err := client.Call("doops_clean_workspace", map[string]interface{}{
		"workspace": workspace,
	})
	if err != nil {
		return fmt.Errorf("清理失败: %v", err)
	}

	return nil
}
