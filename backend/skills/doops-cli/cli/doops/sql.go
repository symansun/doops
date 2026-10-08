package main

import (
	"flag"
	"fmt"
	"strings"
)

type sqlMode string

const (
	sqlModeQuery sqlMode = "query"
	sqlModePlan  sqlMode = "plan"
	sqlModeApply sqlMode = "apply"
	sqlModeDiscover sqlMode = "discover"
)

func runSQLCommand(args []string, sessionName string, servers []Server, configErr error, ss *SessionStore, verbose bool) error {
	flags := flag.NewFlagSet("sql", flag.ContinueOnError)
	flags.SetOutput(nil)
	target := flags.String("target", "", "Configured Gateway target")
	request := flags.String("request", "", "Database request in natural language")
	mode := flags.String("mode", string(sqlModeQuery), "DoSql mode: query, plan, apply, or discover")
	database := flags.String("database", "", "Optional logical database or asset hint")
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
	if strings.TrimSpace(*request) == "" {
		return fmt.Errorf("数据库需求不能为空，请使用 --request")
	}
	parsedMode, err := parseSQLMode(*mode)
	if err != nil {
		return err
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
