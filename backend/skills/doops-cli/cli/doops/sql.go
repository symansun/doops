package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type sqlMode string

const (
	sqlModeQuery    sqlMode = "query"
	sqlModePlan     sqlMode = "plan"
	sqlModeApply    sqlMode = "apply"
	sqlModeDiscover sqlMode = "discover"
)

func runSQLCommand(args []string, sessionName string, servers []Server, configErr error, ss *SessionStore, verbose bool) error {
	flags := flag.NewFlagSet("sql", flag.ContinueOnError)
	flags.SetOutput(nil)
	target := flags.String("target", "", "Configured Gateway target")
	request := flags.String("request", "", "Database request in natural language")
	mode := flags.String("mode", string(sqlModeQuery), "DoSql mode: query, plan, apply, or discover")
	database := flags.String("database", "", "Optional logical database or asset hint")
	root := flags.String("root", "", "Local code/Helm root for --mode discover")
	resourcesFile := flags.String("resources-file", "", "DoSql asset inventory JSON path for --mode discover")
	displayResourcesFile := flags.String("display-resources-file", "", "DoSql display resource JSON path for --mode discover")
	project := flags.String("project", "", "DoSql project ID for --mode discover")
	environment := flags.String("environment", "", "DoSql environment ID for --mode discover")
	script := flags.String("dosql-script", "", "Path to dosql-agent.mjs for --mode discover")
	subSession := flags.String("session", "", "Session ID (can also be set before subcommand)")
	var credentialRefs stringListFlag
	var databaseRefs stringListFlag
	var repositoryRefs stringListFlag
	flags.Var(&credentialRefs, "credential", "Managed credential resource name; repeatable")
	flags.Var(&databaseRefs, "database-ref", "Database asset reference for the conversation; repeatable")
	flags.Var(&repositoryRefs, "repo-ref", "Code repository reference, optionally ID@branch; repeatable")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *subSession != "" && sessionName == "" {
		sessionName = *subSession
	}
	if strings.TrimSpace(sessionName) == "" {
		return fmt.Errorf("-session 必传，请指定会话 ID 以隔离数据库操作上下文")
	}
	if strings.TrimSpace(*target) == "" {
		return fmt.Errorf("--target 必传，请指定 Gateway target")
	}
	if strings.TrimSpace(*request) == "" {
		positional := strings.TrimSpace(strings.Join(flags.Args(), " "))
		if positional != "" {
			*request = positional
		}
	}
	parsedMode, err := parseSQLMode(*mode)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*request) == "" && !(parsedMode == sqlModeDiscover && strings.TrimSpace(*root) != "") {
		return fmt.Errorf("数据库需求不能为空，请使用 --request")
	}
	if parsedMode == sqlModeDiscover && strings.TrimSpace(*root) != "" {
		return runLocalSQLResourceDiscovery(*root, *resourcesFile, *displayResourcesFile, *project, *environment, *script, *target, servers, configErr)
	}
	if (parsedMode == sqlModeQuery || parsedMode == sqlModeApply) && len(credentialRefs) == 0 {
		return fmt.Errorf("--credential 至少需要一个 Gateway 受管数据库凭据资源名")
	}
	instruction, err := appendConversationReferences(buildSQLInstruction(parsedMode, *database, *request), []string(databaseRefs), []string(repositoryRefs))
	if err != nil {
		return err
	}
	return runSkillPrompt("dosql", sqlGatewayOperation(parsedMode), *target, sessionName, instruction, []string(credentialRefs), servers, configErr, ss, verbose)
}

func runLocalSQLResourceDiscovery(root, resourcesFile, displayResourcesFile, project, environment, script, target string, servers []Server, configErr error) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(resourcesFile) == "" || strings.TrimSpace(project) == "" || strings.TrimSpace(environment) == "" || strings.TrimSpace(script) == "" {
		return fmt.Errorf("--mode discover 本地扫描需要 --root、--resources-file、--project、--environment 和 --dosql-script")
	}
	if configErr != nil {
		return configErr
	}
	server := findServer(servers, target)
	if server == nil {
		return fmt.Errorf("server %q not found", target)
	}
	input := map[string]interface{}{
		"operationId": "discover-resources-cli",
		"rootDir":     root, "resourcesPath": resourcesFile,
		"projectId": project, "environmentId": environment,
		"target": map[string]string{"name": server.Name, "cluster": server.Cluster, "instance": server.Instance},
	}
	if strings.TrimSpace(displayResourcesFile) != "" {
		input["displayResourcesPath"] = displayResourcesFile
	}
	tmpDir, err := os.MkdirTemp("", "doops-dosql-discover-")
	if err != nil {
		return fmt.Errorf("create discovery workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	inputPath := filepath.Join(tmpDir, "input.json")
	outputPath := filepath.Join(tmpDir, "output.json")
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode discovery request: %w", err)
	}
	if err := os.WriteFile(inputPath, encoded, 0600); err != nil {
		return fmt.Errorf("write discovery request: %w", err)
	}
	command := exec.Command("node", script, "discover-resources", "--input", inputPath, "--output", outputPath)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("DoSql resource discovery failed: %s", strings.TrimSpace(string(output)))
	}
	result, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("read discovery result: %w", err)
	}
	var response struct {
		Status string      `json:"status"`
		Result interface{} `json:"result"`
		Error  interface{} `json:"error"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return fmt.Errorf("decode discovery result: %w", err)
	}
	if response.Status != "succeeded" {
		return fmt.Errorf("DoSql resource discovery failed: %v", response.Error)
	}
	encodedResult, _ := json.Marshal(response.Result)
	fmt.Printf("dosql_resource_discovery=complete target=%s project=%s environment=%s result=%s\n", target, project, environment, encodedResult)
	return nil
}

func parseSQLMode(value string) (sqlMode, error) {
	switch sqlMode(strings.ToLower(strings.TrimSpace(value))) {
	case sqlModeQuery:
		return sqlModeQuery, nil
	case sqlModePlan:
		return sqlModePlan, nil
	case sqlModeApply:
		return sqlModeApply, nil
	case sqlModeDiscover:
		return sqlModeDiscover, nil
	default:
		return "", fmt.Errorf("unsupported --mode %q; use query, plan, apply, or discover", value)
	}
}

func sqlGatewayOperation(mode sqlMode) string {
	if mode == sqlModeApply {
		return "apply"
	}
	return "ask"
}

func buildSQLInstruction(mode sqlMode, databaseHint, request string) string {
	var purpose string
	switch mode {
	case sqlModeQuery:
		purpose = "只读查询或巡检，不执行数据库变更。"
	case sqlModeDiscover:
		purpose = "扫描当前 Gateway target 的 Kubernetes 非 Secret 元数据，发现数据库实例和可识别数据库，不读取 Secret 值，不执行数据库连接或变更。"
	case sqlModePlan:
		purpose = "只生成可读的变更计划、风险、影响和验证步骤，不执行数据库变更。"
	case sqlModeApply:
		purpose = "执行受控数据库变更；必须先完成 DoSql 人类可读确认、精确目标解析、签名审批和执行后验证，缺少任一证据就明确失败。"
	}
	hint := ""
	if strings.TrimSpace(databaseHint) != "" {
		hint = "\n数据库提示：" + strings.TrimSpace(databaseHint)
	}
	return strings.TrimSpace(fmt.Sprintf(
		"使用 DoSql skill 处理当前 DoOps Gateway target。%s%s\n不要读取或索取密码、连接串或 token；不要绕过 DoSql CLI、journal、evidence 和 Gateway 授权。\n用户需求：%s",
		purpose, hint, strings.TrimSpace(request),
	))
}
