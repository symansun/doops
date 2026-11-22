package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Push 将本地代码无感隔离投递至大模型探针服务，不落宿主机磁盘、不污染用户 Git
func Push(server Server, src, dest string, dryRun bool, extraExcludes []string, sessionID string) error {
	_, err := PushWithCommit(server, src, dest, dryRun, extraExcludes, sessionID)
	return err
}

func PushWithCommit(server Server, src, dest string, dryRun bool, extraExcludes []string, sessionID string) (string, error) {
	return pushWorkspaceSnapshot(server, src, dest, dryRun, extraExcludes, sessionID)
}

func pushWorkspaceSnapshot(server Server, src, dest string, dryRun bool, extraExcludes []string, sessionID string) (string, error) {
	return pushWorkspaceSnapshotWithSource(server, src, dest, dryRun, extraExcludes, sessionID, nil)
}

func pushWorkspaceSnapshotWithSource(
	server Server,
	src, dest string,
	dryRun bool,
	extraExcludes []string,
	sessionID string,
	source *WorkspaceSourceIdentity,
) (string, error) {
	return pushSnapshot(server, src, dest, dryRun, extraExcludes, sessionID, source, "gateway", "", "", nil)
}

func pushSnapshot(
	server Server,
	src, dest string,
	dryRun bool,
	extraExcludes []string,
	sessionID string,
	source *WorkspaceSourceIdentity,
	storage, credential, token string,
	transfer cosTransfer,
) (string, error) {
	// 1. 解析源目录
	src, err := filepath.Abs(src)
	if err != nil {
		return "", fmt.Errorf("解析源路径失败: %v", err)
	}

	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("源路径不存在: %s", src)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("源路径必须是目录: %s", src)
	}

	if !strings.HasSuffix(src, "/") {
		src += "/"
	}

	// 优先级：显式 sessionID > dest 推导
	if sessionID == "" && dest != "" {
		clean := strings.TrimRight(dest, "/")
		sessionID = filepath.Base(clean)
	}
	sessionID = resolveSession(sessionID)

	mode, err := normalizeStorageMode(storage)
	if err != nil {
		return "", err
	}
	if mode == "cos" {
		if strings.TrimSpace(token) == "" {
			return "", fmt.Errorf("gateway_auth_missing")
		}
		if !dryRun {
			var closeTransfer func()
			transfer, closeTransfer, err = ensureCOSTransfer(server, sessionID, token, transfer)
			if err != nil {
				return "", err
			}
			defer closeTransfer()
			if err := transfer.ProbeSnapshot(); err != nil {
				return "", err
			}
		}
	} else {
		token, err = ResolveToken(server)
		if err != nil {
			return "", err
		}
	}
	workspacePath := fmt.Sprintf("/root/ws/%s", sessionID)

	// 2. 建立本地幽灵沙盒
	tmpDir, err := createWorkspaceSnapshotDirectory(sessionID)
	if err != nil {
		return "", fmt.Errorf("创建幽灵临时目录失败: %v", err)
	}
	defer cleanupSignalDirectory(tmpDir)

	start := time.Now()
	fmt.Printf("🧊 正在构建本地隔离快照（大型仓库首次扫描可能较慢）...\n")
	stagedFiles, err := stageWorkspaceSnapshot(src, tmpDir, extraExcludes, dryRun, source)
	if err != nil {
		return "", fmt.Errorf("本地缓存集结失败: %v", err)
	}

	if dryRun {
		fmt.Printf("📋 [DRY-RUN] 预计推送 %d 个文件到远端工作区 %s\n", len(stagedFiles), workspacePath)
		for _, rel := range stagedFiles {
			fmt.Println(rel)
		}
		return "", nil
	}

	// 4. 在幽灵目录中进行纯净 Git 化。gateway 模式再差分推送；cos 模式把同一份快照对象写入 COS。
	fmt.Printf("📦 本地隔离快照整理完成: %d 个文件\n", len(stagedFiles))
	fmt.Printf("🔧 正在初始化幽灵 Git 仓库并生成同步提交...\n")

	localCommit, err := createWorkspaceSnapshotCommit(tmpDir, source)
	if err != nil {
		return "", err
	}
	if mode == "cos" {
		fmt.Printf("🚀 正在把隔离快照上传到 COS 桶 %s: %s (%s)\n", cosBucketName, workspacePath, server.Name)
		status, err := uploadCOSSnapshot(tmpDir, stagedFiles, localCommit, transfer)
		if err != nil {
			return "", err
		}
		elapsed := time.Since(start)
		shortCommit := localCommit
		if len(shortCommit) > 12 {
			shortCommit = shortCommit[:12]
		}
		fmt.Printf("✅ COS 快照已写入 (%s) @ %s\n", elapsed.Round(time.Millisecond), shortCommit)
		if status == "" || status == "synced" {
			fmt.Printf("workspace: synced %s\n", workspacePath)
		} else {
			fmt.Printf("workspace: %s\n", status)
		}
		return localCommit, nil
	}

	fmt.Printf("🚀 进行极速无感增量推送至远端工作区: %s (%s)\n", workspacePath, server.Name)
	sourceManifestBlob := ""
	if source != nil {
		sourceManifestBlob, err = workspaceSourceManifestBlobID(tmpDir, localCommit)
		if err != nil {
			return "", err
		}
	}
	gitURL, err := buildGitRemoteURLForServer(server, sessionID, true)
	if err != nil {
		return "", err
	}

	gitCmds := [][]string{
		{"push", "-f", gitURL, "HEAD:master"},
	}
	gitEnv, cleanupGitAuth, err := prepareGitAuthEnvironment(token)
	if err != nil {
		return "", err
	}
	defer cleanupGitAuth()
	for _, gargs := range gitCmds {
		stepLabel := describeGitStep(gargs)
		if stepLabel != "" {
			fmt.Printf("⏳ %s...\n", stepLabel)
		}

		maxRetries := 1
		if gargs[0] == "push" {
			maxRetries = 3
		}

		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			gitArgs := append([]string{"-c", "credential.helper="}, gargs...)
			cmd := exec.Command("git", gitArgs...)
			cmd.Dir = tmpDir
			cmd.Env = gitEnv

			// 我们只让最后一步 push 原样输出网络传输进度给用户
			if gargs[0] == "push" {
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if attempt > 1 {
					fmt.Printf("⚠️ 网络异常或超时，正在进行第 %d 次重试推送...\n", attempt)
					time.Sleep(2 * time.Second)
				}
			}

			if err := runIsolatedGitCommand(cmd); err != nil {
				lastErr = err
				if attempt < maxRetries && gargs[0] == "push" {
					continue // 准备重推
				}
				return "", fmt.Errorf("幽灵传输阶段执行失败 (%v): %v", redactGitArgs(gargs), lastErr)
			}
			break // 成功则跳出重试循环
		}
	}

	fmt.Printf("🔍 正在确认远端工作区已完成 checkout 并就绪...\n")
	representativeRel := ""
	if len(stagedFiles) > 0 {
		representativeRel = stagedFiles[0]
	}
	if err := waitForWorkspaceReady(
		server,
		token,
		sessionID,
		workspacePath,
		representativeRel,
		localCommit,
		sourceManifestBlob,
		45*time.Second,
	); err != nil {
		return "", fmt.Errorf("push 已完成，但远端工作区未确认就绪: %v", err)
	}

	elapsed := time.Since(start)
	shortCommit := localCommit
	if len(shortCommit) > 12 {
		shortCommit = shortCommit[:12]
	}
	fmt.Printf("✅ 同步完成 (%s) 🎯 远端工作区已就绪: %s @ %s\n", elapsed.Round(time.Millisecond), workspacePath, shortCommit)
	return localCommit, nil
}

func waitForWorkspaceReady(
	server Server,
	token string,
	sessionID string,
	workspacePath string,
	representativeRel string,
	expectedCommit string,
	expectedSourceManifestBlob string,
	timeout time.Duration,
) error {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	readyFile := filepath.ToSlash(filepath.Join(workspacePath, ".doops-ready"))
	representativePath := ""
	if representativeRel != "" {
		representativePath = filepath.ToSlash(filepath.Join(workspacePath, representativeRel))
	}
	deadline := time.Now().Add(timeout)
	mcpClient := NewMCPClient(server, NewSessionStore(), sessionID, false)
	mcpClient.Token = token
	defer mcpClient.Close()

	var lastOut string
	var lastErr string
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		cmd := ""
		if expectedSourceManifestBlob != "" {
			cmd = workspaceCheckoutProofCommand(workspacePath, sessionID, expectedCommit)
		} else {
			cmd = fmt.Sprintf("if [ -f %q ]; then printf 'ready:'; tr -d '\\r\\n' < %q; exit 0; fi", readyFile, readyFile)
			cmd += "; exit 3"
		}
		stdout, err := mcpClient.CallAndCapture("doops_shell", map[string]interface{}{
			"command": cmd,
		})
		lastOut = strings.TrimSpace(stdout)
		lastErr = ""
		if err == nil {
			if expectedSourceManifestBlob != "" {
				if err := validateWorkspaceCheckoutProof(lastOut, expectedCommit, expectedSourceManifestBlob); err == nil {
					return nil
				} else {
					lastErr = err.Error()
				}
			} else if remoteCommit, ready := workspaceReadyCommit(lastOut); ready {
				if remoteCommit == expectedCommit {
					return nil
				}
				if remoteCommit != "" {
					return fmt.Errorf("远端 ready 哨兵存在，但提交不匹配: want=%s got=%s", expectedCommit, remoteCommit)
				}
			}
		} else {
			lastErr = err.Error()
		}
		if attempt == 1 || attempt%3 == 0 {
			fmt.Printf("⏳ 等待远端释放代码到 %s（第 %d 次探测）...\n", workspacePath, attempt)
		}
		time.Sleep(2 * time.Second)
	}

	if lastOut != "" {
		return fmt.Errorf("等待超时，最后一次 ready 输出=%q stderr=%q", lastOut, lastErr)
	}
	if representativePath != "" {
		return fmt.Errorf("等待超时，未发现远端 ready 哨兵 %s（代表文件 %s 不能替代提交校验）", readyFile, representativePath)
	}
	return fmt.Errorf("等待超时，未发现远端 ready 哨兵 %s", readyFile)
}

func workspaceCheckoutProofCommand(workspacePath, sessionID, expectedCommit string) string {
	readyFile := filepath.ToSlash(filepath.Join(workspacePath, ".doops-ready"))
	manifestFile := filepath.ToSlash(filepath.Join(workspacePath, workspaceSourceManifestPath))
	bareRepository := filepath.ToSlash(filepath.Join("/tmp/repos", sessionID+".git"))
	return fmt.Sprintf(
		`if [ -f %q ] && [ -f %q ]; then `+
			`ready=$(tr -d '\r\n' < %q); `+
			`bare=$(git --git-dir=%q rev-parse master 2>/dev/null); `+
			`tree=$(git --git-dir=%q rev-parse %q 2>/dev/null); `+
			`checkout=$(git hash-object %q 2>/dev/null); `+
			`printf 'ready:%%s\nbare:%%s\ntree:%%s\ncheckout:%%s\n' "$ready" "$bare" "$tree" "$checkout"; `+
			`exit 0; fi; exit 3`,
		readyFile,
		manifestFile,
		readyFile,
		bareRepository,
		bareRepository,
		expectedCommit+":"+workspaceSourceManifestPath,
		manifestFile,
	)
}

func validateWorkspaceCheckoutProof(output, expectedCommit, expectedManifestBlob string) error {
	fields := make(map[string]string, 4)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || value == "" {
			return fmt.Errorf("invalid remote workspace proof line %q", line)
		}
		if _, exists := fields[key]; exists {
			return fmt.Errorf("duplicate remote workspace proof field %q", key)
		}
		switch key {
		case "ready", "bare", "tree", "checkout":
			fields[key] = value
		default:
			return fmt.Errorf("unexpected remote workspace proof field %q", key)
		}
	}
	for _, field := range []string{"ready", "bare", "tree", "checkout"} {
		if fields[field] == "" {
			return fmt.Errorf("remote workspace proof is missing %s", field)
		}
		if !validWorkspaceCommit(fields[field]) {
			return fmt.Errorf("remote workspace proof %s is not a Git object ID", field)
		}
	}
	if fields["ready"] != expectedCommit {
		return fmt.Errorf("remote ready commit mismatch: got %s want %s", fields["ready"], expectedCommit)
	}
	if fields["bare"] != expectedCommit {
		return fmt.Errorf("remote bare master mismatch: got %s want %s", fields["bare"], expectedCommit)
	}
	if fields["tree"] != expectedManifestBlob {
		return fmt.Errorf("remote transport tree manifest mismatch: got %s want %s", fields["tree"], expectedManifestBlob)
	}
	if fields["checkout"] != expectedManifestBlob {
		return fmt.Errorf("remote checkout manifest mismatch: got %s want %s", fields["checkout"], expectedManifestBlob)
	}
	return nil
}

func workspaceReadyCommit(output string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ready:") {
			continue
		}
		commit := strings.TrimSpace(strings.TrimPrefix(line, "ready:"))
		return commit, commit != ""
	}
	return "", false
}
