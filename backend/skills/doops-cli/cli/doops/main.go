package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

const cliContractVersion = "doops.sh/cli-contract/v3"

var (
	cliBuildVersion = "dev"
	cliBuildCommit  = "unknown"
)

func main() {
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	sessionName := flag.String("session", "", "Session/Task ID for isolation (REQUIRED)")
	printContract := flag.Bool("contract", false, "Print the machine-readable CLI contract version")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `
Doops 分布式服务器管理工具 (doops.sh CLI)
专为 AI Agent 设计的自然语言运维工具。发布任务必须显式给出 target、服务范围、镜像版本、变更原因和验收条件。

配置文件路径: ~/.agent/skills/doops/config.json（唯一默认配置；DOOPS_CONFIG 可显式覆盖）

用法:
  doops [选项] <命令> [参数]

可用命令:
  list        列出已配置的 Gateway target 路由
  targets     查看 gateway 当前在线的 cluster/instance
  exec        在目标节点工作区执行 Shell 命令 (用于确定性的日常流水线执行)
  ask         发送自然语言指令 (由边缘端智能体完成受控的通用运维协作)
  read        查看目标节点上的小文本文件（不用于下载大文件/二进制）
  write       写入文件到目标服务器
  info        获取节点系统信息 (CPU/内存/磁盘)
  k8s         受限 Kubernetes 观察与运维入口
  credential  管理 Gateway 凭据元数据与版本
  version     查看 CLI 发布版本、提交和协议契约
  session     生成并输出一个新的唯一 Session ID
  push        极速增量推送本地代码到远端沙盒 (固定至 /root/ws/$SESSION)
  pull        基于 Git 拉取远端 session 工作区到本地目录
  clean       清理远端节点上的工作区隔离沙盒
  unlock      管理员强制释放 gateway 上卡住的 target busy 状态
  admin       gateway 管理命令，例如给用户签发 token
  upgrade     预演 doops-agent 自升级；正式替换由另一隔离 target 或外部 GitOps 执行
  add         添加 gateway target
  login       缓存 gateway user token
  logout      登出远程节点
  check       检查线上服务容器配置的镜像与最新构建是否一致

选项:
  -verbose    启用详细日志
  -session    会话/任务ID (无默认值，涉及远程调用的命令必须提供以严格隔离工作空间)
  -contract   输出机器可读的 CLI 契约版本
  -help       显示此帮助信息

	自然语言发布示例:
	  # --apply 是明确的 mutation 授权；任务本身必须给出所有发布事实。
	  doops -session release-20260717 ask --target agent-runtime --apply --msg "在 target agent-runtime 发布服务范围 api，镜像 release-20260717；原因：修复课堂权限；验收：Deployment Ready、实际镜像、Service/Endpoint 和 /health 均通过。"
`)
	}
	flag.Parse()
	if *printContract {
		fmt.Println(cliContractVersion)
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(0)
	}

	command := args[0]
	cmdArgs := args[1:]

	ss := NewSessionStore()
	servers, _, configErr := LoadConfig()

	switch command {
	case "version":
		fmt.Printf("version=%s commit=%s contract=%s\n",
			cliBuildVersion, cliBuildCommit, cliContractVersion)

	case "session":
		b := make([]byte, 8)
		rand.Read(b)
		fmt.Printf("ses_%x\n", b)
		os.Exit(0)

	case "list":
		if configErr != nil {
			if os.IsNotExist(configErr) {
				fmt.Println("No doops nodes configured.")
				fmt.Println("Add a gateway target with: doops add --name <name> --gateway <gateway-url> --cluster <cluster> --instance <instance>")
				return
			}
			fmt.Printf("Error loading config: %v\n", configErr)
			os.Exit(1)
		}
		if err := printServerList(os.Stdout, servers); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

	case "targets":
		var target string
		targetsFlag := flag.NewFlagSet("targets", flag.ExitOnError)
		targetsFlag.StringVar(&target, "target", "", "Configured target to query")
		targetsFlag.Parse(cmdArgs)
		if target == "" {
			fmt.Println("Error: --target is required")
			targetsFlag.Usage()
			os.Exit(1)
		}
		requireConfig(configErr)
		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", target)
			os.Exit(1)
		}
		token := mustResolveToken(*server)
		targets, err := fetchGatewayTargets(server.Gateway, token)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		targets = filterGatewayTargets(targets, server.Cluster, server.Instance)
		if len(targets) == 0 {
			fmt.Printf("Error: target offline: %s/%s\n", server.Cluster, server.Instance)
			os.Exit(1)
		}
		fmt.Printf("%-18s %-18s %-8s %-16s %-7s %-7s %-28s %-24s\n", "CLUSTER", "INSTANCE", "BUSY", "STATUS", "ACTIVE", "QUEUED", "RESOURCES", "LAST_SEEN")
		fmt.Println(strings.Repeat("-", 132))
		for _, item := range targets {
			status := item.Status
			if status == "" {
				if item.Busy {
					status = "busy"
				} else {
					status = "idle"
				}
			}
			if item.BusyReason != "" {
				status += ":" + item.BusyReason
			}
			resources := strings.Join(item.Resources, ",")
			if len(resources) > 27 {
				resources = resources[:24] + "..."
			}
			fmt.Printf("%-18s %-18s %-8v %-16s %-7d %-7d %-28s %-24s\n", item.Cluster, item.Instance, item.Busy, status, item.ActiveOps, item.QueuedOps, resources, item.LastSeen.Format(time.RFC3339))
		}

	case "unlock":
		var target string
		unlockFlag := flag.NewFlagSet("unlock", flag.ExitOnError)
		unlockFlag.StringVar(&target, "target", "", "Configured Gateway target to unlock")
		unlockFlag.Parse(cmdArgs)
		if target == "" {
			fmt.Println("Error: --target is required")
			unlockFlag.Usage()
			os.Exit(1)
		}
		requireConfig(configErr)
		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", target)
			os.Exit(1)
		}
		token := mustResolveToken(*server)
		if err := unlockGatewayTarget(server.Gateway, token, server.Cluster, server.Instance); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Unlocked %s/%s\n", server.Cluster, server.Instance)

	case "admin":
		if len(cmdArgs) >= 1 && cmdArgs[0] == "jobs" {
			runAdminJobs(cmdArgs[1:], servers, configErr)
			return
		}
		if len(cmdArgs) < 2 {
			fmt.Println("Usage: doops admin token create --target <gateway-target> --user <username> [--name label] [--expires 720h] [--save-as target]")
			fmt.Println("       doops admin token list --target <gateway-target>")
			fmt.Println("       doops admin token revoke --target <gateway-target> --id <token-id>")
			fmt.Println("       doops admin operations list --target <gateway-target>")
			fmt.Println("       doops admin operations cancel --target <gateway-target> --id <operation-id>")
			fmt.Println("       doops admin jobs <list|add|run|enable|disable|rm|issues> --target <gateway-target> [...]")
			os.Exit(1)
		}
		if cmdArgs[0] == "operations" && cmdArgs[1] == "list" {
			runAdminOperationsList(cmdArgs[2:], servers, configErr)
			return
		}
		if cmdArgs[0] == "operations" && cmdArgs[1] == "cancel" {
			runAdminOperationCancel(cmdArgs[2:], servers, configErr)
			return
		}
		if cmdArgs[0] == "token" && cmdArgs[1] == "list" {
			runAdminTokensList(cmdArgs[2:], servers, configErr)
			return
		}
		if cmdArgs[0] == "token" && cmdArgs[1] == "revoke" {
			runAdminTokenRevoke(cmdArgs[2:], servers, configErr)
			return
		}
		if cmdArgs[0] != "token" || cmdArgs[1] != "create" {
			fmt.Println("Usage: doops admin token create --target <gateway-target> --user <username> [--name label] [--expires 720h] [--save-as target]")
			fmt.Println("       doops admin token list --target <gateway-target>")
			fmt.Println("       doops admin token revoke --target <gateway-target> --id <token-id>")
			fmt.Println("       doops admin operations list --target <gateway-target>")
			fmt.Println("       doops admin operations cancel --target <gateway-target> --id <operation-id>")
			os.Exit(1)
		}
		var target, user, label, expires, saveAs string
		adminFlag := flag.NewFlagSet("admin token create", flag.ExitOnError)
		adminFlag.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
		adminFlag.StringVar(&user, "user", "", "Gateway user name to issue a token for")
		adminFlag.StringVar(&label, "name", "", "Token label")
		adminFlag.StringVar(&expires, "expires", "", "Optional token TTL, e.g. 720h")
		adminFlag.StringVar(&saveAs, "save-as", "", "Optional configured target name to cache the issued token for")
		adminFlag.Parse(cmdArgs[2:])
		if strings.TrimSpace(target) == "" {
			fmt.Println("Error: --target is required")
			adminFlag.Usage()
			os.Exit(1)
		}
		requireConfig(configErr)
		server := findServer(servers, target)
		if server == nil || strings.TrimSpace(server.Gateway) == "" {
			fmt.Printf("Error: Gateway target '%s' not found.\n", target)
			os.Exit(1)
		}
		gateway := server.Gateway
		token := mustResolveToken(*server)
		if strings.TrimSpace(user) == "" {
			fmt.Println("Error: --user is required")
			adminFlag.Usage()
			os.Exit(1)
		}
		saveGateway := ""
		if strings.TrimSpace(saveAs) != "" {
			saveServer := findServer(servers, saveAs)
			if saveServer == nil || strings.TrimSpace(saveServer.Gateway) == "" {
				fmt.Printf("Error: Gateway target '%s' not found.\n", saveAs)
				os.Exit(1)
			}
			if gatewayAuthKey(saveServer.Gateway) != gatewayAuthKey(gateway) {
				fmt.Println("Error: --save-as target belongs to a different gateway")
				os.Exit(1)
			}
			saveGateway = saveServer.Gateway
		}
		issued, err := GatewayAdminTokenCreate(gateway, token, gatewayAdminTokenCreateRequest{
			User:    user,
			Name:    label,
			Expires: expires,
		})
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("token_type=%s\n", issued.TokenType)
		if issued.Username != "" {
			fmt.Printf("username=%s\n", issued.Username)
		}
		fmt.Printf("token_id=%s\n", issued.TokenID)
		fmt.Printf("token=%s\n", issued.Token)
		fmt.Println("warning=store this token now; gateway only keeps its hash")
		if saveGateway != "" {
			auth, err := LoadAuth()
			if err != nil {
				fmt.Printf("Error loading credentials: %v\n", err)
				os.Exit(1)
			}
			auth.SetGateway(saveGateway, issued.Token)
			if err := auth.Save(); err != nil {
				fmt.Printf("Error saving issued token: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Cached issued token for gateway %s\n", gatewayAuthKey(saveGateway))
		}

	case "k8s":
		req, err := buildK8SRequest(cmdArgs)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		requireConfig(configErr)
		server := findServer(servers, req.Target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", req.Target)
			os.Exit(1)
		}

		if *sessionName == "" {
			fmt.Println("Error: -session 必传，请指定会话 ID 以隔离 K8S 运维操作")
			os.Exit(1)
		}
		token := mustResolveToken(*server)
		client := NewMCPClient(*server, ss, *sessionName, *verbose)
		client.Token = token
		defer client.Close()

		msg, err := runK8SRequest(client, req)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if msg != "" {
			fmt.Println(msg)
		}
		RecordHistory(server.Name, *sessionName, fmt.Sprintf("k8s %s", req.Payload["operation"]))

	case "credential":
		if err := runCredentialCommand(cmdArgs, *sessionName, servers, configErr, os.Stdin, os.Stdout); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

	case "exec", "ask", "write", "read", "info":
		var target, cmdStr, msgStr, path, modelStr, contentStr, fileStr string
		var credentialRefs stringListFlag
		var subSession string
		var apply, background bool
		subFlag := flag.NewFlagSet(command, flag.ExitOnError)
		subFlag.StringVar(&target, "target", "", "Target server name")
		subFlag.StringVar(&subSession, "session", "", "Session ID (can also be set before subcommand)")

		switch command {
		case "exec":
			subFlag.StringVar(&cmdStr, "cmd", "", "Command to execute")
			subFlag.BoolVar(&background, "bg", false, "Run through the agent-managed background task service and wait for completion")
		case "ask":
			subFlag.StringVar(&msgStr, "msg", "", "Instruction")
			subFlag.StringVar(&modelStr, "model", "", "Model to use for this instruction (optional)")
			subFlag.BoolVar(&apply, "apply", false, "Allow the explicitly requested instruction to make authorized changes")
			subFlag.Var(&credentialRefs, "credential", "Managed credential resource name (repeatable; requires --apply)")
		case "write":
			subFlag.StringVar(&path, "path", "", "Destination path")
			subFlag.StringVar(&contentStr, "content", "", "Content to write; for multi-line content prefer --file or stdin")
			subFlag.StringVar(&fileStr, "file", "", "Read content from local file; use - to read stdin")
		case "read":
			subFlag.StringVar(&path, "path", "", "Source path")
		}

		subFlag.Parse(cmdArgs)

		// 智能指令补充：支持位置参数直接作为 msg/cmd
		positionalArgs := subFlag.Args()
		if len(positionalArgs) > 0 {
			combined := strings.Join(positionalArgs, " ")
			if command == "ask" && msgStr == "" {
				msgStr = combined
			} else if command == "exec" && cmdStr == "" {
				cmdStr = combined
			}
		}

		// 子命令中的 -session 回填全局值
		if subSession != "" && *sessionName == "" {
			*sessionName = subSession
		}

		if *sessionName == "" {
			fmt.Println("Error: -session 必传，请指定会话 ID 以隔离工作区 (例如: doops exec -session prod -target node1 -cmd \"...\")")
			os.Exit(1)
		}

		if target == "" {
			fmt.Println("Error: --target 必传，请指定目标服务器名称。")
			os.Exit(1)
		}
		requireConfig(configErr)

		// 指令/命令校验 (支持位置参数提取后)
		if command == "ask" && strings.TrimSpace(msgStr) == "" {
			fmt.Println("Error: 指令内容不能为空。用法: doops ask \"你的指令\"")
			os.Exit(1)
		}
		if command == "ask" && len(credentialRefs) != 0 && !apply {
			fmt.Println("Error: --credential 仅允许与 --apply 一起使用。")
			os.Exit(1)
		}
		if command == "exec" && strings.TrimSpace(cmdStr) == "" {
			fmt.Println("Error: 命令内容不能为空。用法: doops exec \"ls -la\"")
			os.Exit(1)
		}
		if command == "read" && strings.TrimSpace(path) == "" {
			fmt.Println("Error: path 不能为空。doops read 只用于查看远端小文本文件。")
			os.Exit(1)
		}
		if command == "write" && strings.TrimSpace(path) == "" {
			fmt.Println("Error: path 不能为空。用法: doops write --path /remote/file --file ./local 或 echo data | doops write --path /remote/file")
			os.Exit(1)
		}

		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", target)
			os.Exit(1)
		}
		token := mustResolveToken(*server)

		client := NewMCPClient(*server, ss, *sessionName, *verbose)
		client.Token = token
		defer client.Close() // 确保持久 WebSocket 连接在命令结束后释放

		endpoint := fmt.Sprintf("%s -> %s/%s", server.Gateway, server.Cluster, server.Instance)
		fmt.Printf("\033[93m[TARGETING]\033[0m Server: %s (%s), Use: %s\n",
			server.Name, endpoint, server.Use)

		if command == "exec" && background {
			if err := client.ExecBg(cmdStr); err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			RecordHistory(server.Name, *sessionName, "bg "+cmdStr)
			break
		}

		var toolName string
		arguments := make(map[string]interface{})

		switch command {
		case "exec":
			toolName = "doops_shell"
			arguments["command"] = cmdStr
		case "ask":
			toolName = "doops_agent_prompt"
			arguments["instruction"] = msgStr
			if apply {
				workspaceCommit, err := loadWorkspaceCommit(server.Name, *sessionName)
				if err != nil {
					fmt.Printf("Error: %v\n", err)
					os.Exit(1)
				}
				arguments["operation"] = "apply"
				arguments["workspace_commit"] = workspaceCommit
			}
			if len(credentialRefs) != 0 {
				arguments["credential_refs"] = []string(credentialRefs)
			}
			if modelStr != "" {
				arguments["model"] = modelStr
			}
		case "write":
			toolName = "doops_file_write"
			arguments["path"] = path
			content, err := resolveWriteContent(contentStr, fileStr, subFlag.Args(), os.Stdin)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			arguments["content"] = content
		case "read":
			toolName = "doops_file_read"
			arguments["path"] = path
		case "info":
			toolName = "doops_node_info"
		}

		if err := client.Call(toolName, arguments); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		// Log History
		cmdLog := toolName
		if toolName == "doops_shell" {
			cmdLog = cmdStr
		} else if toolName == "doops_agent_prompt" {
			cmdLog = msgStr
		} else if toolName == "doops_file_write" || toolName == "doops_file_read" {
			cmdLog = fmt.Sprintf("%s %s", toolName, path)
		}
		RecordHistory(server.Name, *sessionName, cmdLog)

	case "history":
		historyPath := historyLogPath()

		data, err := os.ReadFile(historyPath)
		if err != nil {
			fmt.Println("No history found.")
			return
		}
		fmt.Println("--- Command History ---")
		fmt.Print(string(data))

	case "login":
		var target, token, username, password, label string
		var tokenStdin bool
		loginFlag := flag.NewFlagSet("login", flag.ExitOnError)
		loginFlag.StringVar(&target, "target", "", "Target server name")
		loginFlag.StringVar(&token, "token", "", "Gateway user token (optional; username/password login can issue one)")
		loginFlag.BoolVar(&tokenStdin, "token-stdin", false, "Read the Gateway user token from stdin")
		loginFlag.StringVar(&username, "username", "", "Gateway username")
		loginFlag.StringVar(&password, "password", "", "Gateway password")
		loginFlag.StringVar(&label, "name", "", "Token label for gateway login")
		loginFlag.Parse(cmdArgs)

		if target == "" {
			fmt.Println("Error: --target is required")
			loginFlag.Usage()
			os.Exit(1)
		}
		requireConfig(configErr)
		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Gateway target '%s' not found.\n", target)
			os.Exit(1)
		}
		gateway := server.Gateway
		if tokenStdin {
			if strings.TrimSpace(token) != "" || strings.TrimSpace(username) != "" || strings.TrimSpace(password) != "" {
				fmt.Println("Error: --token-stdin cannot be combined with token, username, or password arguments")
				os.Exit(1)
			}
			data, err := io.ReadAll(io.LimitReader(os.Stdin, 16*1024))
			if err != nil {
				fmt.Printf("Error reading gateway token from stdin: %v\n", err)
				os.Exit(1)
			}
			token = strings.TrimSpace(string(data))
			if token == "" {
				fmt.Println("Error: gateway token from stdin is empty")
				os.Exit(1)
			}
		}
		if strings.TrimSpace(token) == "" {
			if strings.TrimSpace(username) == "" {
				fmt.Print("Gateway username: ")
				fmt.Scanln(&username)
			}
			if strings.TrimSpace(password) == "" {
				fmt.Printf("Enter gateway password for %s: ", username)
				bytePassword, err := term.ReadPassword(int(syscall.Stdin))
				fmt.Println()
				if err != nil {
					fmt.Printf("Error reading password: %v\n", err)
					os.Exit(1)
				}
				password = string(bytePassword)
			}
			if label == "" {
				label = "doops login"
			}
			newToken, err := GatewayLogin(gateway, username, password, label)
			if err != nil {
				fmt.Printf("Error logging in to gateway: %v\n", err)
				os.Exit(1)
			}
			token = newToken
		}

		auth, err := LoadAuth()
		if err != nil {
			fmt.Printf("Error loading credentials: %v\n", err)
			os.Exit(1)
		}
		auth.SetGateway(gateway, token)
		if err := auth.Save(); err != nil {
			fmt.Printf("Error saving credentials: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully cached gateway token for %s\n", gatewayAuthKey(gateway))

	case "logout":
		var target string
		logoutFlag := flag.NewFlagSet("logout", flag.ExitOnError)
		logoutFlag.StringVar(&target, "target", "", "Target server name")
		logoutFlag.Parse(cmdArgs)

		if target == "" {
			fmt.Println("Error: --target is required")
			logoutFlag.Usage()
			os.Exit(1)
		}
		requireConfig(configErr)
		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Gateway target '%s' not found.\n", target)
			os.Exit(1)
		}
		auth, err := LoadAuth()
		if err != nil {
			fmt.Printf("Error loading credentials: %v\n", err)
			os.Exit(1)
		}
		auth.RemoveGateway(server.Gateway)
		if err := auth.Save(); err != nil {
			fmt.Printf("Error saving credentials: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Logged out from gateway %s\n", gatewayAuthKey(server.Gateway))

	case "add":
		var name, aliases, use, gateway, cluster, instance string
		addFlag := flag.NewFlagSet("add", flag.ExitOnError)
		addFlag.StringVar(&name, "name", "", "Node name (required)")
		addFlag.StringVar(&aliases, "aliases", "", "Comma separated aliases, e.g. jm,jy,oilan")
		addFlag.StringVar(&aliases, "alias", "", "Comma separated aliases, e.g. jm,jy,oilan")
		addFlag.StringVar(&use, "use", "Manually added node", "Description")
		addFlag.StringVar(&gateway, "gateway", "", "Public Gateway URL")
		addFlag.StringVar(&cluster, "cluster", "", "Gateway cluster name (required)")
		addFlag.StringVar(&instance, "instance", "", "Gateway agent instance name (required)")

		addFlag.Parse(cmdArgs)

		name = strings.TrimSpace(name)
		gateway = strings.TrimSpace(gateway)
		cluster = strings.TrimSpace(cluster)
		instance = strings.TrimSpace(instance)
		if name == "" || gateway == "" || cluster == "" || instance == "" {
			fmt.Println("Error: --name, --gateway, --cluster, and --instance are all required and must be non-empty.")
			addFlag.Usage()
			os.Exit(1)
		}
		if configErr != nil {
			if !os.IsNotExist(configErr) {
				fmt.Printf("Error loading config: %v\n", configErr)
				os.Exit(1)
			}
			servers = nil
		}

		newServer := Server{
			Name:     name,
			Aliases:  normalizeAliases([]string{aliases}),
			Use:      use,
			Gateway:  gateway,
			Cluster:  cluster,
			Instance: instance,
		}

		updated := false
		for i, s := range servers {
			if serverMatchesTarget(s, name) {
				servers[i] = newServer
				updated = true
				break
			}
		}
		if conflict := findAliasConflict(servers, newServer); conflict != "" {
			fmt.Printf("Error: alias/name conflict with existing target '%s'.\n", conflict)
			os.Exit(1)
		}
		if conflict := findRouteConflict(servers, newServer); conflict != "" {
			fmt.Printf("Error: gateway route %s/%s is already configured as target '%s'.\n", newServer.Cluster, newServer.Instance, conflict)
			os.Exit(1)
		}
		if !updated {
			servers = append(servers, newServer)
		}
		if err := saveServers(servers); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
			os.Exit(1)
		}
		if updated {
			fmt.Printf("✅ Node '%s' updated successfully.\n", name)
		} else {
			fmt.Printf("✅ Gateway target '%s' (%s/%s via %s) added successfully.\n", name, newServer.Cluster, newServer.Instance, gateway)
		}

	case "push":
		var target, src, dest string
		var dryRun bool
		var pushSession string
		pushFlag := flag.NewFlagSet("push", flag.ExitOnError)
		pushFlag.StringVar(&target, "target", "", "Target server name")
		pushFlag.StringVar(&src, "src", ".", "Local source directory")
		pushFlag.StringVar(&dest, "dest", "", "Remote destination (用于推导 session 隔离标识)")
		pushFlag.BoolVar(&dryRun, "dry-run", false, "Preview only, don't actually sync")
		pushFlag.StringVar(&pushSession, "session", "", "Session ID (can also be set before subcommand)")
		pushFlag.Parse(cmdArgs)

		// 子命令中的 -session 回填全局值
		if pushSession != "" && *sessionName == "" {
			*sessionName = pushSession
		}

		if target == "" {
			fmt.Println("Error: --target is required")
			pushFlag.Usage()
			os.Exit(1)
		}
		requireConfig(configErr)

		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", target)
			os.Exit(1)
		}

		commit, err := PushWithCommit(*server, src, dest, dryRun, nil, *sessionName)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if !dryRun {
			if err := saveWorkspaceCommit(server.Name, *sessionName, commit); err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
		}
		RecordHistory(server.Name, *sessionName, fmt.Sprintf("push %s -> %s", src, dest))

	case "pull":
		var target, dest string
		var pullSession string
		pullFlag := flag.NewFlagSet("pull", flag.ExitOnError)
		pullFlag.StringVar(&target, "target", "", "Target server name")
		pullFlag.StringVar(&dest, "dest", "", "Local destination directory (default: ./<session>)")
		pullFlag.StringVar(&pullSession, "session", "", "Session ID (can also be set before subcommand)")
		pullFlag.Parse(cmdArgs)

		if pullSession != "" && *sessionName == "" {
			*sessionName = pullSession
		}
		if target == "" {
			fmt.Println("Error: --target is required")
			pullFlag.Usage()
			os.Exit(1)
		}
		if *sessionName == "" {
			fmt.Println("Error: -session 必传，请指定要拉取的远端工作区")
			os.Exit(1)
		}
		requireConfig(configErr)

		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", target)
			os.Exit(1)
		}
		if err := Pull(*server, dest, *sessionName); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		RecordHistory(server.Name, *sessionName, fmt.Sprintf("pull -> %s", dest))

	case "clean":
		var target, workspace, cleanSession string
		cleanFlag := flag.NewFlagSet("clean", flag.ExitOnError)
		cleanFlag.StringVar(&target, "target", "", "Target server name")
		cleanFlag.StringVar(&workspace, "workspace", "", "Workspace name to clean")
		cleanFlag.StringVar(&cleanSession, "session", "", "Session ID (can also be set before subcommand)")
		cleanFlag.Parse(cmdArgs)
		if cleanSession != "" && *sessionName == "" {
			*sessionName = cleanSession
		}

		if target == "" || workspace == "" {
			fmt.Println("Error: --target and --workspace are required")
			cleanFlag.Usage()
			os.Exit(1)
		}
		if *sessionName == "" {
			fmt.Println("Error: -session 必传，请指定会话 ID")
			os.Exit(1)
		}
		requireConfig(configErr)

		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", target)
			os.Exit(1)
		}

		token := mustResolveToken(*server)
		client := NewMCPClient(*server, ss, *sessionName, *verbose)
		client.Token = token
		defer client.Close()

		if err := Clean(client, workspace); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		RecordHistory(server.Name, *sessionName, fmt.Sprintf("clean workspace=%s", workspace))

	case "upgrade":
		var target, image, mode, namespace, workload, container, upgradeSession string
		var dryRun bool
		upgradeFlag := flag.NewFlagSet("upgrade", flag.ExitOnError)
		upgradeFlag.StringVar(&target, "target", "", "Configured Gateway target")
		upgradeFlag.StringVar(&image, "image", "", "New doops-agent image")
		upgradeFlag.StringVar(&mode, "mode", "auto", "Upgrade mode: auto,k8s,docker")
		upgradeFlag.StringVar(&namespace, "namespace", "", "Kubernetes namespace for DaemonSet mode")
		upgradeFlag.StringVar(&workload, "workload", "", "Kubernetes workload, e.g. daemonset/doops-agent")
		upgradeFlag.StringVar(&container, "container", "", "Container name")
		upgradeFlag.StringVar(&upgradeSession, "session", "", "Session ID")
		upgradeFlag.BoolVar(&dryRun, "dry-run", false, "Preview the external reconciliation target; direct agent self-upgrade is prohibited")
		upgradeFlag.Parse(cmdArgs)
		if upgradeSession != "" && *sessionName == "" {
			*sessionName = upgradeSession
		}
		if *sessionName == "" {
			*sessionName = fmt.Sprintf("upgrade_%d", time.Now().Unix())
		}
		if target == "" || image == "" {
			fmt.Println("Error: --target and --image are required")
			upgradeFlag.Usage()
			os.Exit(1)
		}
		requireConfig(configErr)
		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", target)
			os.Exit(1)
		}
		if err := UpgradeAgents(*server, UpgradeOptions{
			Image:     image,
			Mode:      mode,
			Namespace: namespace,
			Workload:  workload,
			Container: container,
			DryRun:    dryRun,
			Session:   *sessionName,
		}, *verbose); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

	case "check":
		var target, deployment, namespace, image, checkSession string
		checkFlag := flag.NewFlagSet("check", flag.ExitOnError)
		checkFlag.StringVar(&target, "target", "", "Target server name")
		checkFlag.StringVar(&deployment, "deployment", "", "Deployment name (e.g. openwork)")
		checkFlag.StringVar(&namespace, "namespace", "default", "Kubernetes namespace (e.g. oilan-system)")
		checkFlag.StringVar(&image, "image", "", "Image name (e.g. registry.example.com/oilan-system/openwork)")
		checkFlag.StringVar(&checkSession, "session", "", "Session ID (can also be set before subcommand)")
		checkFlag.Parse(cmdArgs)
		if checkSession != "" && *sessionName == "" {
			*sessionName = checkSession
		}

		if target == "" || deployment == "" || image == "" {
			fmt.Println("Error: --target, --deployment and --image are required")
			checkFlag.Usage()
			os.Exit(1)
		}
		if *sessionName == "" {
			fmt.Println("Error: -session 必传，请指定会话 ID")
			os.Exit(1)
		}
		requireConfig(configErr)

		server := findServer(servers, target)
		if server == nil {
			fmt.Printf("Error: Server '%s' not found.\n", target)
			os.Exit(1)
		}

		token := mustResolveToken(*server)
		client := NewMCPClient(*server, ss, *sessionName, *verbose)
		client.Token = token
		defer client.Close()

		if err := CheckDeployment(client, namespace, deployment, image); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		RecordHistory(server.Name, *sessionName, fmt.Sprintf("check %s/%s", namespace, deployment))

	default:
		fmt.Printf("Unknown command: %s\n", command)
		flag.Usage()
		os.Exit(1)
	}
}

func printServerList(w io.Writer, servers []Server) error {
	if err := validateGatewayServers(servers); err != nil {
		return err
	}
	fmt.Fprintf(w, "%-20s %-18s %-28s %-30s %-30s\n", "NAME", "ALIASES", "GATEWAY", "CLUSTER/INSTANCE", "USE")
	fmt.Fprintln(w, strings.Repeat("-", 130))
	for _, s := range servers {
		fmt.Fprintf(w, "%-20s %-18s %-28s %-30s %-30s\n",
			s.Name,
			strings.Join(s.Aliases, ","),
			s.Gateway,
			strings.TrimSpace(s.Cluster)+"/"+strings.TrimSpace(s.Instance),
			s.Use,
		)
	}
	return nil
}

func findServer(servers []Server, name string) *Server {
	for _, s := range servers {
		if serverMatchesTarget(s, name) {
			return &s
		}
	}
	return nil
}

func serverMatchesTarget(server Server, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if server.Name == target {
		return true
	}
	for _, alias := range server.Aliases {
		if alias == target {
			return true
		}
	}
	return false
}

func findAliasConflict(servers []Server, candidate Server) string {
	names := append([]string{candidate.Name}, candidate.Aliases...)
	for _, server := range servers {
		if server.Name == candidate.Name {
			continue
		}
		for _, name := range names {
			if serverMatchesTarget(server, name) {
				return server.Name
			}
		}
	}
	return ""
}

func findRouteConflict(servers []Server, candidate Server) string {
	candidateGateway := strings.TrimRight(strings.TrimSpace(candidate.Gateway), "/")
	candidateCluster := strings.TrimSpace(candidate.Cluster)
	candidateInstance := strings.TrimSpace(candidate.Instance)
	if candidateGateway == "" || candidateCluster == "" || candidateInstance == "" {
		return ""
	}
	for _, server := range servers {
		if server.Name == candidate.Name {
			continue
		}
		if strings.TrimRight(strings.TrimSpace(server.Gateway), "/") == candidateGateway &&
			strings.TrimSpace(server.Cluster) == candidateCluster &&
			strings.TrimSpace(server.Instance) == candidateInstance {
			return server.Name
		}
	}
	return ""
}

func runAdminOperationsList(args []string, servers []Server, configErr error) {
	gateway, token := resolveAdminGatewayFlags("admin operations list", args, servers, configErr, nil)
	ops, err := GatewayAdminOperationsList(gateway, token)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%-12s %-8s %-18s %-18s %-8s %-16s %-8s %-8s %s\n", "ID", "KIND", "CLUSTER", "INSTANCE", "ACTION", "SESSION", "AGE", "USER", "SUMMARY")
	fmt.Println(strings.Repeat("-", 132))
	for _, op := range ops {
		summary := op.CommandSummary
		if len(summary) > 48 {
			summary = summary[:45] + "..."
		}
		fmt.Printf("%-12s %-8s %-18s %-18s %-8s %-16s %-8ds %-8s %s\n", op.ID, op.Kind, op.Cluster, op.Instance, op.Action, op.Session, op.AgeSeconds, op.UserID, summary)
	}
}

func runAdminOperationCancel(args []string, servers []Server, configErr error) {
	var opID string
	gateway, token := resolveAdminGatewayFlags("admin operations cancel", args, servers, configErr, &opID)
	if err := GatewayAdminOperationCancel(gateway, token, opID); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Canceled operation %s\n", opID)
}

func runAdminTokensList(args []string, servers []Server, configErr error) {
	fs := flag.NewFlagSet("admin token list", flag.ExitOnError)
	target := fs.String("target", "", "Configured gateway target whose token/gateway should be used")
	fs.Parse(args)
	if strings.TrimSpace(*target) == "" {
		fmt.Println("Error: --target is required")
		fs.Usage()
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, *target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Printf("Error: Gateway target '%s' not found.\n", *target)
		os.Exit(1)
	}
	tokens, err := GatewayAdminTokensList(server.Gateway, mustResolveToken(*server), "user")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	for _, token := range tokens {
		fmt.Printf("token_id=%s kind=%s user=%s name=%s prefix=%s revoked=%t expires_at=%s created_at=%s\n",
			token.ID, token.Kind, token.UserName, token.Name, token.Prefix,
			token.Revoked, token.ExpiresAt, token.CreatedAt)
	}
}

func runAdminTokenRevoke(args []string, servers []Server, configErr error) {
	var target, id string
	fs := flag.NewFlagSet("admin token revoke", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.StringVar(&id, "id", "", "Token id to revoke")
	fs.Parse(args)
	if strings.TrimSpace(target) == "" || strings.TrimSpace(id) == "" {
		fmt.Println("Error: --target and --id are required")
		fs.Usage()
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Printf("Error: Gateway target '%s' not found.\n", target)
		os.Exit(1)
	}
	if err := GatewayAdminTokenRevoke(server.Gateway, mustResolveToken(*server), id); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("token_id=%s revoked=true\n", strings.TrimSpace(id))
}

func resolveAdminGatewayFlags(name string, args []string, servers []Server, configErr error, opID *string) (string, string) {
	var target string
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	if opID != nil {
		fs.StringVar(opID, "id", "", "Active operation id")
	}
	fs.Parse(args)
	if strings.TrimSpace(target) == "" {
		fmt.Println("Error: --target is required")
		fs.Usage()
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Printf("Error: Gateway target '%s' not found.\n", target)
		os.Exit(1)
	}
	if opID != nil && strings.TrimSpace(*opID) == "" {
		fmt.Println("Error: --id is required")
		fs.Usage()
		os.Exit(1)
	}
	return server.Gateway, mustResolveToken(*server)
}

func requireConfig(err error) {
	if err == nil {
		return
	}
	fmt.Printf("Error: no doops node config found: %v\n", err)
	fmt.Println("Add a gateway target with: doops add --name <name> --gateway <gateway-url> --cluster <cluster> --instance <instance>")
	os.Exit(1)
}

func ResolveToken(server Server) (string, error) {
	if strings.TrimSpace(server.Gateway) == "" {
		return "", fmt.Errorf("target %q has no Gateway route", server.Name)
	}
	auth, err := LoadAuth()
	if err != nil {
		return "", err
	}
	if p := auth.GetGateway(server.Gateway); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no login for Gateway %s; run `doops login --target %s`",
		gatewayAuthKey(server.Gateway), server.Name)
}

func mustResolveToken(server Server) string {
	token, err := ResolveToken(server)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	return token
}
