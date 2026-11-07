package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type installCapability struct {
	name     string
	required bool
}

type installCapabilityResult struct {
	Name   string
	OK     bool
	Output string
}

func defaultInstallCapabilities() []installCapability {
	return []installCapability{
		{
			name:     "agent process",
			required: true,
		},
		{
			name:     "container runtime",
			required: true,
		},
		{
			name:     "kubectl",
			required: false,
		},
		{
			name:     "buildkit",
			required: false,
		},
	}
}

func SSHExec(host, user, password, port, command string) (int, string, string, error) {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return 1, "", "", err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return 1, "", "", err
	}
	defer sess.Close()

	var stdoutBuf, stderrBuf strings.Builder
	sess.Stdout = &stdoutBuf
	sess.Stderr = &stderrBuf

	// Run with a max timeout; background commands (&) return quickly
	done := make(chan error, 1)
	go func() { done <- sess.Run(command) }()

	select {
	case err = <-done:
		// command finished
	case <-time.After(30 * time.Second):
		// background / slow command — detach and continue
		return 0, stdoutBuf.String(), stderrBuf.String(), nil
	}

	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return exitErr.ExitStatus(), stdoutBuf.String(), stderrBuf.String(), nil
		}
		return 1, stdoutBuf.String(), stderrBuf.String(), err
	}

	return 0, stdoutBuf.String(), stderrBuf.String(), nil
}

func SCPFile(host, user, password, port, localPath, remotePath string) error {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return err
	}
	defer client.Close()

	// 1. Upload file
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session failed: %v", err)
	}

	f, err := os.Open(localPath)
	if err != nil {
		session.Close()
		return fmt.Errorf("open local file failed: %v", err)
	}
	defer f.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return err
	}

	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdin, f)
		stdin.Close()
		copyDone <- err
	}()

	if err := session.Run(fmt.Sprintf("cat > %s", remotePath)); err != nil {
		return fmt.Errorf("upload failed: %v", err)
	}

	if err := <-copyDone; err != nil {
		return fmt.Errorf("io.Copy failed: %v", err)
	}
	session.Close()

	// 2. Chmod +x
	session2, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session2.Close()
	return session2.Run(fmt.Sprintf("chmod +x %s", remotePath))
}

func InstallAgent(name, ip, user, sshPassword, sshPort, agentPort, binaryPath string, local bool, agentToken string, llm LLMConfig) error {
	fmt.Printf("🚀 Installing doops-agent on %s...\n", ip)
	if strings.TrimSpace(agentToken) == "" {
		token, err := generateInstallToken()
		if err != nil {
			return fmt.Errorf("failed to generate agent token: %v", err)
		}
		agentToken = token
	}

	// Prepare Env Strings
	envVars := ""
	if llm.BaseURL != "" {
		envVars += fmt.Sprintf(" API_BASE_URL='%s'", llm.BaseURL)
	}
	if llm.APIKey != "" {
		envVars += fmt.Sprintf(" OPENAI_API_KEY='%s'", llm.APIKey)
	}
	if llm.Model != "" {
		envVars += fmt.Sprintf(" DO_AGENT_MODEL='%s'", llm.Model)
	} else {
		envVars += " DO_AGENT_MODEL='openai/gpt-5.4'"
	}

	if binaryPath != "" {
		// Binary Installation Mode
		remoteBin := "/tmp/doops-agent"
		remoteSkills := "/tmp/skills"
		fmt.Printf("📦 Mode: Binary Upload (%s -> %s)\n", binaryPath, remoteBin)

		// 1. Stop old agent & Clean up
		fmt.Print("  [1/4] Stopping old agent & Cleaning up... ")
		SSHExec(ip, user, sshPassword, sshPort, fmt.Sprintf("pkill -f doops-agent || true; docker stop doops-agent 2>/dev/null || true; rm -f %s; rm -rf %s", remoteBin, remoteSkills))
		fmt.Printf("Done\n")

		// 2. Upload binary
		fmt.Print("  [2/4] Uploading binary... ")
		if err := SCPFile(ip, user, sshPassword, sshPort, binaryPath, remoteBin); err != nil {
			fmt.Printf("Failed\n")
			return err
		}
		fmt.Printf("Done\n")

		// 3. Sync Skills directory
		fmt.Print("  [3/4] Syncing skills directory... ")
		// Skills are in the same parent directory as binaryPath/../../skills ??
		// No, usually they are at the project root.
		// If binaryPath is <root>/agent/doops-agent, skills is at <root>/agent/skills
		localSkillsDir := filepath.Join(filepath.Dir(binaryPath), "skills")
		if _, err := os.Stat(localSkillsDir); err == nil {
			tarPath := "/tmp/doops-skills.tar"
			cmd := exec.Command("tar", "-cf", tarPath, "-C", filepath.Dir(binaryPath), "skills")
			if err := cmd.Run(); err != nil {
				fmt.Printf("Warning: tar failed: %v\n", err)
			} else {
				if err := SCPFile(ip, user, sshPassword, sshPort, tarPath, "/tmp/skills.tar"); err == nil {
					SSHExec(ip, user, sshPassword, sshPort, "mkdir -p /tmp && tar -xf /tmp/skills.tar -C /tmp/ && rm /tmp/skills.tar")
					os.Remove(tarPath)
				} else {
					fmt.Printf("Warning: Skill sync failed: %v\n", err)
				}
			}
		} else {
			fmt.Printf("Warning: local skills dir %s not found\n", localSkillsDir)
		}
		fmt.Printf("Done\n")

		// 4. Start new agent
		fmt.Print("  [4/4] Starting new agent... ")
		startCmd := fmt.Sprintf("nohup env%s %s -port %s -token '%s' > /tmp/doops-agent.log 2>&1 </dev/null &", envVars, remoteBin, agentPort, agentToken)
		_, _, _, err := SSHExec(ip, user, sshPassword, sshPort, "cd /tmp && "+startCmd)
		if err != nil {

			fmt.Printf("Failed\n")
			return err
		}
		fmt.Printf("Done\n")

	} else {

		// Docker Installation Mode
		// 本仓库不发布公共镜像；请通过 DOOPS_AGENT_IMAGE 指定可拉取的 doops-agent 镜像。
		image := os.Getenv("DOOPS_AGENT_IMAGE")
		if image == "" {
			return fmt.Errorf("DOOPS_AGENT_IMAGE 未设置：请指定可拉取的 doops-agent 镜像")
		}

		// Prepare Docker Env Args
		dockerEnv := ""
		if llm.BaseURL != "" {
			dockerEnv += fmt.Sprintf(" -e API_BASE_URL='%s'", llm.BaseURL)
		}
		if llm.APIKey != "" {
			dockerEnv += fmt.Sprintf(" -e OPENAI_API_KEY='%s'", llm.APIKey)
		}
		if llm.Model != "" {
			dockerEnv += fmt.Sprintf(" -e DO_AGENT_MODEL='%s'", llm.Model)
		} else {
			dockerEnv += " -e DO_AGENT_MODEL='openai/gpt-5.4'"
		}

		// Steps implementation
		steps := []struct {
			desc string
			cmd  string
		}{
			{"Checking SSH connectivity", "echo 'SSH OK'"},
			{"Checking Docker", "docker --version || (curl -fsSL https://get.docker.com | sh)"},
			{"Pulling Image", fmt.Sprintf("docker pull %s", image)},
			{"Cleaning up existing agent", "docker stop doops-agent 2>/dev/null || true; docker rm doops-agent 2>/dev/null || true"},
			{"Starting Agent", fmt.Sprintf(`docker run -d --name doops-agent --privileged --pid=host --network=host --restart=unless-stopped %s %s -token '%s'`, dockerEnv, image, agentToken)},
		}
		for i, step := range steps {
			fmt.Printf("  [%d/%d] %s...\n", i+1, len(steps), step.desc)
			exitCode, _, _, err := SSHExec(ip, user, sshPassword, sshPort, step.cmd)
			if err != nil || exitCode != 0 {
				return fmt.Errorf("step '%s' failed: %v", step.desc, err)
			}
		}
	}

	fmt.Printf("\n✅ Agent is running on %s:%s\n", ip, agentPort)

	results, err := verifyInstalledAgentCapabilities(name, ip, user, sshPassword, sshPort, agentPort, agentToken)
	if err != nil {
		return err
	}
	printInstallCapabilityResults(results)

	// Update local config
	servers, _ := LoadServers()
	updated := false
	for i, s := range servers {
		if s.Name == name {
			servers[i].IP = ip
			servers[i].Port = agentPort
			servers[i].Use = "Dynamically installed node"
			servers[i].Token = agentToken
			// 保留原有 SSHUser/SSHPort（install 参数中已有 user/sshPort）
			if servers[i].SSHUser == "" {
				servers[i].SSHUser = user
			}
			if servers[i].SSHPort == "" {
				servers[i].SSHPort = sshPort
			}
			if servers[i].SSHPassword == "" {
				servers[i].SSHPassword = sshPassword
			}
			updated = true
			break
		}
	}
	if !updated {
		servers = append(servers, Server{Name: name, IP: ip, Port: agentPort, Use: "Dynamically installed node", Token: agentToken, SSHUser: user, SSHPort: sshPort, SSHPassword: sshPassword})
	}

	if err := saveServers(servers); err != nil {
		return fmt.Errorf("failed to update config: %v", err)
	}

	fmt.Printf("✅ Config updated. Node '%s' is ready!\n", name)
	return nil
}

func verifyInstalledAgentCapabilities(name, ip, user, sshPassword, sshPort, agentPort, agentToken string) ([]installCapabilityResult, error) {
	server := Server{
		Name:  name,
		IP:    ip,
		Port:  agentPort,
		Token: agentToken,
		Use:   "post-install capability check",
	}
	client := NewMCPClient(server, NewSessionStore(), "install_capability_check", false)
	client.Token = agentToken
	defer client.Close()

	output, err := client.CallAndCapture("doops_node_info", map[string]interface{}{})
	if err != nil {
		return []installCapabilityResult{
			{Name: "agent process", OK: false, Output: err.Error()},
		}, fmt.Errorf("post-install capability check failed: cannot query installed agent (%v)", err)
	}

	checks := defaultInstallCapabilities()
	results := make([]installCapabilityResult, 0, len(checks))
	for _, check := range checks {
		ok, snippet := parseCapabilityStatus(output, check.name)
		results = append(results, installCapabilityResult{
			Name:   check.name,
			OK:     ok,
			Output: snippet,
		})
		if check.required && !ok {
			if snippet == "" {
				snippet = "no diagnostic output"
			}
			return results, fmt.Errorf("post-install capability check failed: %s (%s)", check.name, snippet)
		}
	}
	return results, nil
}

func parseCapabilityStatus(output, capability string) (bool, string) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch capability {
		case "agent process":
			if strings.Contains(trimmed, "System Info") || strings.Contains(trimmed, "Uptime") {
				return true, "agent responded to doops_node_info"
			}
		case "container runtime":
			if strings.HasPrefix(trimmed, "container-runtime:") {
				return !strings.Contains(trimmed, "MISSING"), trimmed
			}
		case "kubectl":
			if strings.HasPrefix(trimmed, "kubectl:") || strings.HasPrefix(trimmed, "kubeconfig:") {
				return !(strings.Contains(trimmed, "MISSING")), trimmed
			}
		case "buildkit":
			if strings.HasPrefix(trimmed, "buildctl:") || strings.HasPrefix(trimmed, "buildkit-sock:") {
				if strings.Contains(trimmed, "MISSING") {
					return false, trimmed
				}
				return true, trimmed
			}
		}
	}
	return false, ""
}

func printInstallCapabilityResults(results []installCapabilityResult) {
	if len(results) == 0 {
		return
	}
	fmt.Println("🔎 Post-install capability check:")
	for _, result := range results {
		status := "OK"
		if !result.OK {
			status = "MISSING"
		}
		fmt.Printf("  - %s: %s\n", result.Name, status)
		if strings.TrimSpace(result.Output) != "" {
			fmt.Printf("    %s\n", strings.ReplaceAll(strings.TrimSpace(result.Output), "\n", "\n    "))
		}
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func generateInstallToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
