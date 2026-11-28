package main

import (
	"fmt"
	"os"
	"strings"
)

type sqlDirectInput struct {
	SQL          string
	Verification string
	Database     string
	DryRun       bool
}

// resolveSQLText accepts exactly one of an inline SQL string and a file; both
// reach the Agent as the same verbatim text, so the two paths cannot diverge.
func resolveSQLText(inline, file, inlineFlag, fileFlag string) (string, error) {
	inline, file = strings.TrimSpace(inline), strings.TrimSpace(file)
	if inline != "" && file != "" {
		return "", fmt.Errorf("%s 和 %s 只能二选一", inlineFlag, fileFlag)
	}
	if file == "" {
		return inline, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("读取 %s %s: %w", fileFlag, file, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("%s %s 是空文件", fileFlag, file)
	}
	return text, nil
}

func buildSQLDirectInput(mode sqlMode, sqlText, sqlFile, verify, verifyFile, database string, dryRun bool) (*sqlDirectInput, error) {
	statement, err := resolveSQLText(sqlText, sqlFile, "--sql", "--file")
	if err != nil {
		return nil, err
	}
	verification, err := resolveSQLText(verify, verifyFile, "--verify", "--verify-file")
	if err != nil {
		return nil, err
	}
	if statement == "" {
		if verification != "" || dryRun {
			return nil, fmt.Errorf("--verify/--verify-file/--dry-run 需要同时提供 --sql 或 --file")
		}
		return nil, nil
	}
	if mode != sqlModeQuery && mode != sqlModeApply {
		return nil, fmt.Errorf("--sql/--file 只支持 --mode query 或 --mode apply")
	}
	if mode == sqlModeQuery && (verification != "" || dryRun) {
		return nil, fmt.Errorf("--verify/--verify-file/--dry-run 只用于 --mode apply")
	}
	return &sqlDirectInput{SQL: statement, Verification: verification, Database: strings.TrimSpace(database), DryRun: dryRun}, nil
}

func sqlDirectAuditDescription(mode sqlMode, input *sqlDirectInput) string {
	action := "只读查询"
	if mode == sqlModeApply {
		action = "数据库变更"
		if input.DryRun {
			action = "数据库变更干跑（回滚）"
		}
	}
	return fmt.Sprintf("DoSql 直接执行 %s，SQL 原样提交，不经过模型。", action)
}

func (input *sqlDirectInput) promptArguments() map[string]interface{} {
	arguments := map[string]interface{}{"sql": input.SQL}
	if input.Verification != "" {
		arguments["sql_verification"] = input.Verification
	}
	if input.Database != "" {
		arguments["sql_database"] = input.Database
	}
	if input.DryRun {
		arguments["sql_dry_run"] = true
	}
	return arguments
}

func attachSQLGovernanceArgs(arguments map[string]interface{}, lockTimeout, stmtTimeout, releaseTag, policyFile string, overrideRules []string, overrideReason string) error {
	if v := strings.TrimSpace(lockTimeout); v != "" {
		arguments["sql_lock_timeout"] = v
	}
	if v := strings.TrimSpace(stmtTimeout); v != "" {
		arguments["sql_statement_timeout"] = v
	}
	if v := strings.TrimSpace(releaseTag); v != "" {
		arguments["sql_release_tag"] = v
	}
	if p := resolveDefaultPolicyFile(policyFile); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("读取 --policy %s: %w", p, err)
		}
		arguments["sql_policy"] = string(raw)
	}
	if len(overrideRules) > 0 {
		if strings.TrimSpace(overrideReason) == "" {
			return fmt.Errorf("--override-rule 需要同时提供 --override-reason")
		}
		arguments["sql_override_rules"] = overrideRules
		arguments["sql_override_reason"] = strings.TrimSpace(overrideReason)
	}
	return nil
}
