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
	sessionName := flag.String("session", "", "Session/Task ID for operation context (REQUIRED)")
	printContract := flag.Bool("contract", false, "Print the machine-readable CLI contract version")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `
Doops 分布式服务器管理工具 (doops.sh CLI)
专为 AI Agent 设计的自然语言运维工具。发布任务必须显式给出 target、服务范围、镜像版本、变更原因和验收条件。

配置文件路径: ~/.agent/skills/doops/config.json（唯一默认配置；DOOPS_CONFIG 可显式覆盖）

发布对象:
  普通业务 workload: ask --apply，不要求本机 kubeconfig 或先执行 push
  已具备自升级控制器的 Helm Agent 镜像: upgrade，由集群内 Job 执行
  Gateway 控制面: scripts/deploy-gateway.sh，不通过 target 发布自身

用法:
  doops [选项] <命令> [参数]

可用命令:
  list        列出已配置的 Gateway target 路由
  targets     查看 gateway 当前在线的 cluster/instance
  capabilities 检查 target Agent 的协议能力
  exec        在目标节点工作区执行 Shell 命令 (用于确定性的日常流水线执行)
  ssh         通过结构化 argv 代理 SSH，避免多层命令引号被远端 shell 重解析
  ask         发送自然语言指令 (由边缘端智能体完成受控的通用运维协作)
  read        查看目标节点上的小文本文件（不用于下载大文件/二进制）
  write       写入文件到目标服务器
  info        获取节点系统信息 (CPU/内存/磁盘)
  k8s         受限 Kubernetes 观察与运维入口
	credential  管理 Gateway 凭据元数据与版本
	registry    通用 OCI 镜像 registry sync（DoOps 目标机执行）
  version     查看 DoOps 发布身份（release tag 和精确提交）
  session     生成并输出一个新的唯一 Session ID
  push        极速增量推送本地代码到远端沙盒 (固定至 /root/ws/$SESSION)
  pull        基于 Git 拉取远端 session 工作区到本地目录
  clean       清理远端节点上的工作区隔离沙盒
  unlock      管理员仅重连空闲 target，不强制释放活动任务
  admin       gateway 管理命令：用户、target 授权和 API key
              admin audit optimize：显式、受审计地维护查询索引
              admin operations cancel：仅请求取消，不代表任务已停止
              admin operations reconcile：管理员人工核对已停止状态，非自动接管
  upgrade     升级 doops-agent；--dry-run 只预演，正式升级由目标集群内的 Job 执行
  add         添加 gateway target
  remove      仅在 target 当前不可见时删除本机 gateway target 配置
  login       缓存 gateway user token
  logout      登出远程节点
  check       检查线上服务容器配置的镜像与最新构建是否一致

选项:
  -verbose    启用详细日志
  -session    会话/任务ID (远程调用须提供；用于操作上下文，不要求先 push 或创建源码快照)
  -contract   输出机器可读的 CLI 契约版本
  -help       显示此帮助信息

	自然语言发布示例:
	  # --apply 是明确的 mutation 授权；任务本身必须给出所有发布事实。
	  doops -session release-<commit> ask --target <target> --apply --msg "在 target <target> 发布服务范围 api，release app，namespace apps，chart https://charts.example/app.tgz，镜像 <repository>@sha256:<digest>；原因：修复课堂权限；验收：最终 Deployment spec/generation/副本、实际镜像、Service/Endpoint 和 /health 均通过。"
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
	case "registry":
		if err := runRegistryCommand(cmdArgs, *sessionName, servers, configErr, os.Stdout); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("release=%s commit=%s\n", cliBuildVersion, cliBuildCommit)

	case "capabilities":
		var target, required string
		capabilitiesFlag := flag.NewFlagSet("capabilities", flag.ExitOnError)
		capabilitiesFlag.StringVar(&target, "target", "", "Configured Gateway target")
		capabilitiesFlag.StringVar(&required, "require", "", "Required protocol: structuredSSH, executionLifecycle")
		capabilitiesFlag.Parse(cmdArgs)
		if target == "" || required == "" {
			fmt.Println("Error: --target and --require are required")
			capabilitiesFlag.Usage()
			os.Exit(1)
		}
		if err := validateRequiredProtocol(required); err != nil {
			fmt.Printf("Error: %v\n", err)
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
		if err := client.RequireProtocol(required); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("target=%s protocol=%s status=ready\n", target, required)

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
			fmt.Println(gatewayTargetUnavailableMessage(server.Cluster, server.Instance))
			os.Exit(1)
		}
		fmt.Printf("%-18s %-18s %-8s %-16s %-7s %-7s %-40s %-28s %-24s\n", "CLUSTER", "INSTANCE", "BUSY", "STATUS", "ACTIVE", "QUEUED", "RELEASE_COMMIT", "RESOURCES", "LAST_SEEN")
		fmt.Println(strings.Repeat("-", 178))
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
			releaseCommit := item.ReleaseCommit
			if releaseCommit == "" {
				releaseCommit = "<unknown>"
			}
			fmt.Printf("%-18s %-18s %-8v %-16s %-7d %-7d %-40s %-28s %-24s\n", item.Cluster, item.Instance, item.Busy, status, item.ActiveOps, item.QueuedOps, releaseCommit, resources, item.LastSeen.Format(time.RFC3339))
		}

	case "unlock":
		var target string
		unlockFlag := flag.NewFlagSet("unlock", flag.ExitOnError)
		unlockFlag.StringVar(&target, "target", "", "Configured Gateway target to reconnect only when idle; does not force-release active work")
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
		fmt.Printf("Idle target reconnect requested for %s/%s; active work is not force-released.\n", server.Cluster, server.Instance)

	case "admin":
		if len(cmdArgs) >= 2 && cmdArgs[0] == "audit" && cmdArgs[1] == "optimize" {
			runAdminAuditOptimize(cmdArgs[2:], servers, configErr, *sessionName)
			return
		}
		if len(cmdArgs) >= 1 && cmdArgs[0] == "jobs" {
			runAdminJobs(cmdArgs[1:], servers, configErr)
			return
		}
		if len(cmdArgs) >= 1 && cmdArgs[0] == "user" {
			runAdminUsers(cmdArgs[1:], servers, configErr)
			return
		}
		if len(cmdArgs) >= 1 && cmdArgs[0] == "grant" {
			runAdminGrants(cmdArgs[1:], servers, configErr)
			return
		}
		if len(cmdArgs) < 2 {
			fmt.Println("Usage: doops admin user <list|create|password|disable> ...")
			fmt.Println("       doops admin grant <list|add|delete> ...")
			fmt.Println("Usage: doops admin token create --target <gateway-target> --user <username> [--name label] [--expires 720h] [--save-as target]")
			fmt.Println("       doops admin token list --target <gateway-target>")
			fmt.Println("       doops admin token revoke --target <gateway-target> --id <token-id>")
			fmt.Println("       doops admin operations list --target <gateway-target>")
			fmt.Println("       doops admin operations cancel --target <gateway-target> --id <operation-id>")
			fmt.Println("       doops admin operations reconcile --target <gateway-target> --id audit-N --confirm-stopped --reason <reason> --evidence <evidence> (admin-only, human-reviewed stopped state; no automated takeover)")
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
		if cmdArgs[0] == "operations" && cmdArgs[1] == "reconcile" {
			runAdminOperationReconcile(cmdArgs[2:], servers, configErr)
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
			fmt.Println("Usage: doops admin user <list|create|password|disable> ...")
			fmt.Println("       doops admin grant <list|add|delete> ...")
			fmt.Println("Usage: doops admin token create --target <gateway-target> --user <username> [--name label] [--expires 720h] [--save-as target]")
			fmt.Println("       doops admin token list --target <gateway-target>")
			fmt.Println("       doops admin token revoke --target <gateway-target> --id <token-id>")
			fmt.Println("       doops admin operations list --target <gateway-target>")
			fmt.Println("       doops admin operations cancel --target <gateway-target> --id <operation-id>")
			fmt.Println("       doops admin operations reconcile --target <gateway-target> --id audit-N --confirm-stopped --reason <reason> --evidence <evidence> (admin-only, human-reviewed stopped state; no automated takeover)")
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

	case "exec", "ssh", "ask", "write", "read", "info":
		var target, cmdStr, msgStr, path, modelStr, contentStr, fileStr, sshHost string
		var credentialRefs stringListFlag
		var sshOptions stringListFlag
		var subSession string
		var apply, background bool
		subFlag := flag.NewFlagSet(command, flag.ExitOnError)
		subFlag.StringVar(&target, "target", "", "Target server name")
		subFlag.StringVar(&subSession, "session", "", "Session ID (can also be set before subcommand)")

		switch command {
		case "exec":
			subFlag.StringVar(&cmdStr, "cmd", "", "Command to execute")
			subFlag.BoolVar(&background, "bg", false, "Run through one streaming background task RPC until the process stops; requires executionLifecycle; no status polling or task retries")
		case "ssh":
			subFlag.StringVar(&sshHost, "host", "", "SSH destination, for example ops@app.internal")
			subFlag.Var(&sshOptions, "option", "Allowed SSH flag or key=value (repeatable, e.g. --option=BatchMode=yes)")
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
			fmt.Println("Error: -session 必传，请指定会话 ID 以隔离操作上下文 (例如: doops exec -session prod -target node1 -cmd \"...\")")
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
		if command == "exec" && looksLikeSSHCommand(cmdStr) {
			fmt.Println("Error: 多层 SSH 必须使用结构化命令: doops ssh --host <user@host> -- <remote-command> [args...]")
			os.Exit(1)
		}
		if command == "ssh" && strings.TrimSpace(sshHost) == "" {
			fmt.Println("Error: --host 必传。用法: doops ssh --target <t> --host <user@host> -- bash -lc '...'")
			os.Exit(1)
		}
		if command == "ssh" {
			normalizedOptions, err := normalizeSSHOptions(sshOptions)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			sshOptions = normalizedOptions
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
		case "ssh":
			toolName = "doops_ssh"
			arguments["host"] = sshHost
			arguments["options"] = []string(sshOptions)
			arguments["command"] = positionalArgs
		case "ask":
			toolName = "doops_agent_prompt"
			arguments["instruction"] = msgStr
			if apply {
				arguments["operation"] = "apply"
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

		if toolName == "doops_ssh" {
			if err := client.RequireProtocol("structuredSSH"); err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
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

	case "remove", "rm":
		var target string
		removeFlag := flag.NewFlagSet("remove", flag.ExitOnError)
		removeFlag.StringVar(&target, "target", "", "Configured Gateway target to remove")
		removeFlag.Parse(cmdArgs)
		if strings.TrimSpace(target) == "" {
			fmt.Println("Error: --target is required")
			removeFlag.Usage()
			os.Exit(1)
		}
		if *sessionName == "" {
			fmt.Println("Error: -session 必传，请指定会话 ID")
			os.Exit(1)
		}
		requireConfig(configErr)
		index := -1
		for i, candidate := range servers {
			if serverMatchesTarget(candidate, target) {
				index = i
				break
			}
		}
		if index < 0 {
			fmt.Printf("Error: Gateway target '%s' not found.\n", target)
			os.Exit(1)
		}
		server := servers[index]
		token := mustResolveToken(server)
		liveTargets, err := fetchGatewayTargets(server.Gateway, token)
		if err != nil {
			fmt.Printf("Error: cannot verify target is offline; refusing to remove '%s': %v\n", target, err)
			os.Exit(1)
		}
		if len(filterGatewayTargets(liveTargets, server.Cluster, server.Instance)) > 0 {
			fmt.Printf("Error: target %s/%s is online; refusing to remove '%s'.\n", server.Cluster, server.Instance, target)
			os.Exit(1)
		}
		remaining, removed, err := removeServer(servers, target)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if err := saveServers(remaining); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Removed offline Gateway target '%s' (%s/%s).\n", removed.Name, removed.Cluster, removed.Instance)

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

		if err := Push(*server, src, dest, dryRun, nil, *sessionName); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
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
		var target, image, mode, namespace, workload, container, release, upgradeSession string
		var dryRun, rollback bool
		upgradeFlag := flag.NewFlagSet("upgrade", flag.ExitOnError)
		upgradeFlag.StringVar(&target, "target", "", "Configured Gateway target")
		upgradeFlag.StringVar(&image, "image", "", "Pinned doops-agent image, repository@sha256:<digest>")
		upgradeFlag.StringVar(&mode, "mode", "auto", "Upgrade mode: auto or k8s")
		upgradeFlag.StringVar(&namespace, "namespace", "", "Optional namespace; must match the agent namespace")
		upgradeFlag.StringVar(&workload, "workload", "", "Optional deployment name; must match the Helm release")
		upgradeFlag.StringVar(&container, "container", "", "Container name; managed upgrades use doops-agent")
		upgradeFlag.StringVar(&release, "release", "", "Optional Helm release; discovered when omitted")
		upgradeFlag.StringVar(&upgradeSession, "session", "", "Session ID")
		upgradeFlag.BoolVar(&dryRun, "dry-run", false, "Preview parameters only; does not verify live release, mounts or permissions, or create a Job")
		upgradeFlag.BoolVar(&rollback, "rollback", false, "Roll the managed Helm release back to the previous successful revision")
		upgradeFlag.Parse(cmdArgs)
		if upgradeSession != "" && *sessionName == "" {
			*sessionName = upgradeSession
		}
		if *sessionName == "" {
			fmt.Println("Error: -session is required for agent upgrade")
			upgradeFlag.Usage()
			os.Exit(1)
		}
		if target == "" || (image == "" && !rollback) {
			fmt.Println("Error: --target is required; also provide --image unless using --rollback")
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
			Release:   release,
			DryRun:    dryRun,
			Rollback:  rollback,
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

func gatewayTargetUnavailableMessage(cluster, instance string) string {
	return fmt.Sprintf(
		"Error: target unavailable: %s/%s is not visible through this Gateway login; it may be offline or the user may lack an explicit target grant. Check the Gateway token, exact target route, and grant before concluding the cluster is down.",
		strings.TrimSpace(cluster), strings.TrimSpace(instance),
	)
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
	server, token := resolveAdminTargetFlags("admin operations list", args, servers, configErr, nil)
	ops, err := GatewayAdminOperationsList(server.Gateway, token, server.Cluster, server.Instance)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	ops = filterGatewayOperations(ops, server.Cluster, server.Instance)
	fmt.Printf("%-12s %-8s %-18s %-24s %-18s %-8s %-18s %-18s %-8s %-16s %-8s %-8s %s\n", "ID", "AUDIT_ID", "STATE", "RESOURCE", "BLOCKED_BY", "KIND", "CLUSTER", "INSTANCE", "ACTION", "SESSION", "AGE", "USER", "SUMMARY")
	fmt.Println(strings.Repeat("-", 132))
	for _, op := range ops {
		summary := op.CommandSummary
		if len(summary) > 48 {
			summary = summary[:45] + "..."
		}
		fmt.Printf("%-12s %-8d %-18s %-24s %-18s %-8s %-18s %-18s %-8s %-16s %-8ds %-8s %s\n", op.ID, op.AuditID, op.State, op.Resource, strings.Join(op.BlockedBy, ","), op.Kind, op.Cluster, op.Instance, op.Action, op.Session, op.AgeSeconds, op.UserID, summary)
	}
}

func runAdminOperationCancel(args []string, servers []Server, configErr error) {
	var opID string
	server, token := resolveAdminTargetFlags("admin operations cancel", args, servers, configErr, &opID)
	ops, err := GatewayAdminOperationsList(server.Gateway, token, server.Cluster, server.Instance)
	if err != nil {
		fmt.Printf("Error: cannot verify operation target: %v\n", err)
		os.Exit(1)
	}
	if !operationBelongsToTarget(ops, opID, server.Cluster, server.Instance) {
		fmt.Printf("Error: operation %q is not active on target %s/%s; refusing cross-target cancellation.\n",
			strings.TrimSpace(opID), strings.TrimSpace(server.Cluster), strings.TrimSpace(server.Instance))
		os.Exit(1)
	}
	if err := GatewayAdminOperationCancel(server.Gateway, token, opID, server.Cluster, server.Instance); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("id=%s cancel_requested=true state=cancel_requested; cancellation requested, not confirmed stopped.\n", strings.TrimSpace(opID))
}

func runAdminOperationReconcile(args []string, servers []Server, configErr error) {
	var target string
	var reconcile gatewayAdminOperationReconcileRequest
	fs := flag.NewFlagSet("admin operations reconcile", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Admin-only manual reconciliation of human-reviewed stopped state; no automated takeover.")
		fs.PrintDefaults()
	}
	fs.StringVar(&target, "target", "", "Configured Gateway target whose admin login should be used")
	fs.StringVar(&reconcile.ID, "id", "", "Unresolved audit operation id (audit-N)")
	fs.BoolVar(&reconcile.ConfirmStopped, "confirm-stopped", false, "Confirm a human has reviewed evidence that the operation has stopped (required)")
	fs.StringVar(&reconcile.Reason, "reason", "", "Nonempty reason for manual reconciliation (required)")
	fs.StringVar(&reconcile.Evidence, "evidence", "", "Nonempty human-reviewed evidence that the operation has stopped (required)")
	fs.Parse(args)
	if strings.TrimSpace(target) == "" {
		fmt.Println("Error: --target is required")
		fs.Usage()
		os.Exit(1)
	}
	if err := reconcile.validate(); err != nil {
		fmt.Printf("Error: %v\n", err)
		fs.Usage()
		os.Exit(1)
	}
	if fs.NArg() != 0 {
		fmt.Println("Error: unexpected positional arguments for manual reconciliation")
		fs.Usage()
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Printf("Error: Gateway target '%s' not found.\n", target)
		os.Exit(1)
	}
	if err := GatewayAdminOperationReconcile(server.Gateway, mustResolveToken(*server), server.Cluster, server.Instance, reconcile); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("id=%s state=resolved_manually; human-reviewed stopped state recorded by admin; no automated takeover.\n", reconcile.ID)
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

func resolveAdminTargetFlags(name string, args []string, servers []Server, configErr error, opID *string) (Server, string) {
	var target string
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	if opID != nil {
		fs.StringVar(opID, "id", "", "Active operation id to request cancellation for; acknowledgment does not confirm it has stopped")
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
	return *server, mustResolveToken(*server)
}

func resolveAdminGatewayFlags(name string, args []string, servers []Server, configErr error, opID *string) (string, string) {
	server, token := resolveAdminTargetFlags(name, args, servers, configErr, opID)
	return server.Gateway, token
}

func filterGatewayOperations(ops []gatewayActiveOperation, cluster, instance string) []gatewayActiveOperation {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	filtered := make([]gatewayActiveOperation, 0, len(ops))
	for _, op := range ops {
		if strings.TrimSpace(op.Cluster) == cluster && strings.TrimSpace(op.Instance) == instance {
			filtered = append(filtered, op)
		}
	}
	return filtered
}

func operationBelongsToTarget(ops []gatewayActiveOperation, id, cluster, instance string) bool {
	id = strings.TrimSpace(id)
	for _, op := range filterGatewayOperations(ops, cluster, instance) {
		if op.ID == id {
			return true
		}
	}
	return false
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
