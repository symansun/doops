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
	sqlModeInit     sqlMode = "init"
	sqlModeQuery    sqlMode = "query"
	sqlModePlan     sqlMode = "plan"
	sqlModeApply    sqlMode = "apply"
	sqlModeDiscover sqlMode = "discover"
	sqlModeInspect  sqlMode = "inspect"
	sqlModeVersions sqlMode = "versions"
	sqlModeHistory  sqlMode = "history"
	sqlModeLint     sqlMode = "lint"
	sqlModeDiff     sqlMode = "diff"
	sqlModeDataDiff sqlMode = "datadiff"
	sqlModeStatus   sqlMode = "status"
	sqlModeMigrate  sqlMode = "migrate"
	sqlModeBackfill sqlMode = "backfill"
	sqlModePack     sqlMode = "pack"
)

// sqlBindingNext turns a DoSql binding failure into the command that fixes it.
// Preserve the requested database while the operator repairs its binding.
func sqlBindingNext(err error, target string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	importSecret := "doops credential configure --target " + target +
		" --name <db-credential> --type opaque --scope personal --namespace <ns> --use database" +
		" --from-secret <secret> --keys <engine-specific-keys>"
	switch {
	case strings.Contains(message, "dosql_database_binding_missing"),
		strings.Contains(message, "dosql_credential_missing"):
		return fmt.Errorf("%s", withNext(message,
			"doops sql --target "+target+" --mode discover --request 'list database workloads'   # engine, host, database name",
			"doops k8s --target "+target+" get deployment <workload> -n <ns> -o yaml   # read env valueFrom.secretKeyRef.name; Secret values stay hidden",
			"doops guide database   # choose Secret keys for the discovered engine",
			importSecret+"   # imports the Secret on the Agent; no password is typed or printed",
			"doops sql --target "+target+" --mode query --sql 'select 1'"))
	case strings.Contains(message, "dosql_database_not_bound"):
		return fmt.Errorf("%s", withNext(message,
			"doops credential list --target "+target+"   # databases already bound",
			"doops guide database   # choose Secret keys for the requested database's engine",
			importSecret+"   # bind the requested database, then rerun with the original --database; do not switch databases"))
	case strings.Contains(message, "dosql_database_required"):
		return fmt.Errorf("%s", withNext(message, "rerun with --database <database> (the database part of engine/database in the list above)"))
	case strings.Contains(message, "credential_version_not_found"):
		return fmt.Errorf("%s", withNext(message,
			"doops credential configure --target "+target+" --id <id> --namespace <ns> --use database --from-secret <secret> --keys <keys>"))
	}
	return err
}

func runSQLCommand(args []string, sessionName string, servers []Server, configErr error, ss *SessionStore, verbose bool) (runErr error) {
	flags := flag.NewFlagSet("sql", flag.ContinueOnError)
	flags.SetOutput(nil)
	target := flags.String("target", "", "Configured Gateway target")
	request := flags.String("request", "", "Database request in natural language (optional for discover)")
	mode := flags.String("mode", string(sqlModeQuery), "DoSql mode: init, query, plan, apply, discover, inspect, diff, datadiff, status, migrate, backfill, pack, versions, history, or lint")
	projectDir := flags.String("project-dir", ".", "Project root directory for --mode init (scaffolds .dosql/)")
	database := flags.String("database", "", "Optional logical database or asset hint")
	diffFrom := flags.String("from", "", "Source schema ref for --mode diff (latest, live, or v<N>)")
	diffTo := flags.String("to", "", "Target schema ref for --mode diff (live, latest, or v<N>)")
	sourceDB := flags.String("source", "", "Source database name on the same target for --mode datadiff")
	dataDiffCols := flags.String("columns", "", "Comma-separated column list for --mode datadiff (auto-inspected when omitted)")
	chunkSize := flags.Int64("chunk-size", 10000, "Key range chunk size for --mode datadiff")
	generateFix := flags.Bool("generate-fix", false, "Emit INSERT/UPDATE/DELETE fix SQL for --mode datadiff")
	migrateDir := flags.String("dir", "", "Migration directory containing V00N__desc.sql / .mongo.json for --mode status|migrate")
	packOut := flags.String("out", "", "Output migration directory for --mode pack")
	packDesc := flags.String("desc", "", "Short description slug for --mode pack (e.g. add_order_status)")
	historyFile := flags.String("history-file", "", "Optional local history JSON file for offline --mode pack")
	allSessions := flags.Bool("all-sessions", false, "Include history across all sessions for --mode history or --mode pack")
	baseline := flags.Int64("baseline", 0, "Mark an existing database at this baseline version on first --mode migrate")
	resume := flags.Bool("resume", false, "Resume a dirty/partially-committed migration from the last applied statement (--mode migrate)")
	backfillTable := flags.String("table", "", "Target table name for --mode backfill or --mode datadiff")
	backfillKey := flags.String("key-column", "id", "Integer primary/unique key column for --mode backfill")
	backfillSet := flags.String("set", "", "SQL SET assignment clause for --mode backfill (e.g. 'status_v2 = status')")
	backfillWhere := flags.String("where", "", "Optional extra WHERE predicate for --mode backfill")
	backfillBatch := flags.Int64("batch-size", 1000, "Rows per transaction chunk for --mode backfill")
	backfillThrottle := flags.String("throttle", "", "Sleep duration between backfill chunks (e.g. 50ms)")
	backfillStartID := flags.Int64("start-after", 0, "Resume backfill cursor strictly after this key ID")
	root := flags.String("root", "", "Local code/Helm root for --mode discover")
	resourcesFile := flags.String("resources-file", "", "DoSql asset inventory JSON path for --mode discover")
	displayResourcesFile := flags.String("display-resources-file", "", "DoSql display resource JSON path for --mode discover")
	project := flags.String("project", "", "DoSql project ID for --mode discover")
	environment := flags.String("environment", "", "DoSql environment ID for --mode discover")
	script := flags.String("dosql-script", "", "Path to dosql-agent.mjs for --mode discover")
	subSession := flags.String("session", "", "Session ID (can also be set before subcommand)")
	sqlText := flags.String("sql", "", "SQL to execute verbatim; one or more statements separated by ;")
	sqlFile := flags.String("file", "", "SQL file to execute verbatim")
	verify := flags.String("verify", "", "Read-only verification SQL after the batch (--mode apply); implicit-commit DDL cannot be rolled back")
	verifyFile := flags.String("verify-file", "", "File holding the read-only verification SQL (--mode apply)")
	dryRun := flags.Bool("dry-run", false, "Execute a rollback-capable batch and verification, then roll back (--mode apply); rejects implicit-commit DDL")
	lockTimeout := flags.String("lock-timeout", "", "Per-request lock wait timeout (e.g. 10s, max 5m; sets both row & MDL timeout on MySQL)")
	statementTimeout := flags.String("statement-timeout", "", "Per-request statement timeout (e.g. 5m, max 30m)")
	releaseTag := flags.String("release-tag", "", "Optional release tag recorded in DoSql version/statement history")
	policyFile := flags.String("policy", "", "Path to .dosql/policy.yaml for Policy-as-Code governance checks")
	var overrideRules stringListFlag
	flags.Var(&overrideRules, "override-rule", "Policy rule ID to override with audit reason (repeatable)")
	overrideReason := flags.String("override-reason", "", "Audit reason required when using --override-rule")
	copyFrom := flags.String("copy-from", "", "Copy whole tables from this source database into --database (--mode apply, same engine, destination tables must exist)")
	copyTables := flags.String("tables", "", "Comma-separated table names for --copy-from")
	// Without --credential the Gateway binds every database credential on the
	// target and the Agent selects one by --database. --credential picks one
	// bound credential by name or ID, e.g. a migration account for DDL.
	credential := flags.String("credential", "", "Bound database credential to use, by resource name or ID (--sql/--file only)")
	var databaseRefs stringListFlag
	var repositoryRefs stringListFlag
	flags.Var(&databaseRefs, "database-ref", "Database asset reference for the conversation; repeatable")
	flags.Var(&repositoryRefs, "repo-ref", "Code repository reference, optionally ID@branch; repeatable")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	parsedMode, err := parseSQLMode(*mode)
	if err != nil {
		return err
	}
	if parsedMode == sqlModeInit {
		base := strings.TrimSpace(*projectDir)
		if base == "" {
			base = "."
		}
		created, skipped, err := initDoSQLProjectWorkspace(base)
		if err != nil {
			return err
		}
		fmt.Printf("dosql_init=ok dir=%s created=%d skipped=%d\n", filepath.Join(base, defaultDoSQLDirName), len(created), len(skipped))
		for _, p := range created {
			fmt.Printf("  created: %s\n", p)
		}
		for _, p := range skipped {
			fmt.Printf("  exists:  %s\n", p)
		}
		return nil
	}
	if *subSession != "" && sessionName == "" {
		sessionName = *subSession
	}
	sessionName = resolveSession(sessionName)
	if strings.TrimSpace(*target) == "" {
		return fmt.Errorf("--target 必传，请指定 Gateway target")
	}
	defer func() { runErr = sqlBindingNext(runErr, strings.TrimSpace(*target)) }()
	if strings.TrimSpace(*request) == "" {
		positional := strings.TrimSpace(strings.Join(flags.Args(), " "))
		if positional != "" {
			*request = positional
		}
	}
	selectedCredential := strings.TrimSpace(*credential)
	if strings.TrimSpace(*copyFrom) != "" {
		if selectedCredential != "" {
			return fmt.Errorf("--credential 不能与 --copy-from 同用：复制需要源库和目标库两个凭据")
		}
		if parsedMode != sqlModeApply {
			return fmt.Errorf("--copy-from 只用于 --mode apply")
		}
		if *sqlText != "" || *sqlFile != "" || strings.TrimSpace(*request) != "" || *verify != "" || *verifyFile != "" {
			return fmt.Errorf("--copy-from 不能与 --sql/--file/--request/--verify 同用")
		}
		if strings.TrimSpace(*database) == "" {
			return fmt.Errorf("--copy-from 需要 --database 指定目标库")
		}
		var tables []string
		for _, name := range strings.Split(*copyTables, ",") {
			if name = strings.TrimSpace(name); name != "" {
				tables = append(tables, name)
			}
		}
		if len(tables) == 0 {
			return fmt.Errorf("--copy-from 需要 --tables a,b")
		}
		extra := map[string]interface{}{
			"sql_copy_from":   strings.TrimSpace(*copyFrom),
			"sql_copy_tables": tables,
			"sql_database":    strings.TrimSpace(*database),
		}
		if *dryRun {
			extra["sql_dry_run"] = true
		}
		return runSkillPromptWith("dosql", "apply", *target, sessionName,
			fmt.Sprintf("DoSql 跨库复制 %s -> %s：%s，由 Agent 分批读写，不经过模型。", *copyFrom, *database, strings.Join(tables, ",")),
			"auto", nil, extra, servers, configErr, ss, verbose)
	}
	// Pack session history (live from target or from --history-file) into V00N__desc.sql + dosql.sum
	if parsedMode == sqlModePack {
		outDir := resolveDefaultMigrationsDir(*packOut)
		if outDir == "" || strings.TrimSpace(*packDesc) == "" {
			return fmt.Errorf("--mode pack 需要 --desc <slug> 与 --out <dir>（或在已运行 doops sql --mode init 的项目中省略 --out 默认写入 .dosql/migrations）")
		}
		var rawHistory []byte
		if strings.TrimSpace(*historyFile) != "" {
			data, err := os.ReadFile(strings.TrimSpace(*historyFile))
			if err != nil {
				return fmt.Errorf("读取 --history-file: %w", err)
			}
			rawHistory = data
		} else {
			arguments := map[string]interface{}{"sql_action": "history"}
			if strings.TrimSpace(*database) != "" {
				arguments["sql_database"] = strings.TrimSpace(*database)
			}
			if selectedCredential != "" {
				arguments["sql_credential"] = selectedCredential
			}
			if *allSessions {
				arguments["sql_all_sessions"] = true
			}
			captured, err := runSkillPromptCapture("dosql", "ask", *target, sessionName, "DoSql history for pack", "auto", arguments, servers, configErr, ss, verbose)
			if err != nil {
				return err
			}
			rawHistory = []byte(captured)
		}
		outPath, count, err := packCLIHistoryToDir(outDir, strings.TrimSpace(*packDesc), "", rawHistory)
		if err != nil {
			return err
		}
		fmt.Printf("dosql_pack=ok file=%s statements=%d\n", outPath, count)
		return nil
	}
	// Direct governance/inspection/migration/backfill/datadiff actions
	if parsedMode == sqlModeInspect || parsedMode == sqlModeVersions || parsedMode == sqlModeHistory ||
		parsedMode == sqlModeLint || parsedMode == sqlModeDiff || parsedMode == sqlModeDataDiff ||
		parsedMode == sqlModeStatus || parsedMode == sqlModeMigrate || parsedMode == sqlModeBackfill {
		arguments := map[string]interface{}{"sql_action": string(parsedMode)}
		if strings.TrimSpace(*database) != "" {
			arguments["sql_database"] = strings.TrimSpace(*database)
		}
		if selectedCredential != "" {
			arguments["sql_credential"] = selectedCredential
		}
		if parsedMode == sqlModeHistory && *allSessions {
			arguments["sql_all_sessions"] = true
		}
		if parsedMode == sqlModeDiff {
			if v := strings.TrimSpace(*diffFrom); v != "" {
				arguments["sql_diff_from"] = v
			}
			if v := strings.TrimSpace(*diffTo); v != "" {
				arguments["sql_diff_to"] = v
			}
		}
		if parsedMode == sqlModeDataDiff {
			if strings.TrimSpace(*sourceDB) == "" || strings.TrimSpace(*backfillTable) == "" {
				return fmt.Errorf("--mode datadiff 需要 --source <源库> 和 --table <表名>")
			}
			arguments["sql_datadiff_source"] = strings.TrimSpace(*sourceDB)
			arguments["sql_datadiff_table"] = strings.TrimSpace(*backfillTable)
			arguments["sql_datadiff_key"] = strings.TrimSpace(*backfillKey)
			if strings.TrimSpace(*dataDiffCols) != "" {
				var cols []string
				for _, c := range strings.Split(*dataDiffCols, ",") {
					if c = strings.TrimSpace(c); c != "" {
						cols = append(cols, c)
					}
				}
				arguments["sql_datadiff_columns"] = cols
			}
			if *chunkSize > 0 {
				arguments["sql_datadiff_chunk"] = *chunkSize
			}
			if *generateFix {
				arguments["sql_datadiff_fix"] = true
			}
		}
		if parsedMode == sqlModeBackfill {
			if strings.TrimSpace(*backfillTable) == "" || strings.TrimSpace(*backfillSet) == "" {
				return fmt.Errorf("--mode backfill 需要 --table <表名> 和 --set '<赋值子句>'")
			}
			arguments["sql_backfill_table"] = strings.TrimSpace(*backfillTable)
			arguments["sql_backfill_key"] = strings.TrimSpace(*backfillKey)
			arguments["sql_backfill_set"] = strings.TrimSpace(*backfillSet)
			if v := strings.TrimSpace(*backfillWhere); v != "" {
				arguments["sql_backfill_where"] = v
			}
			if *backfillBatch > 0 {
				arguments["sql_backfill_batch"] = *backfillBatch
			}
			if v := strings.TrimSpace(*backfillThrottle); v != "" {
				arguments["sql_backfill_throttle"] = v
			}
			if *backfillStartID > 0 {
				arguments["sql_backfill_start_id"] = *backfillStartID
			}
			if *dryRun {
				arguments["sql_dry_run"] = true
			}
		}
		if parsedMode == sqlModeStatus || parsedMode == sqlModeMigrate {
			dir := resolveDefaultMigrationsDir(*migrateDir)
			if dir != "" {
				manifest, err := loadCLIMigrationDirJSON(dir, parsedMode == sqlModeMigrate)
				if err != nil {
					return err
				}
				arguments["sql_migrations"] = manifest
			} else if parsedMode == sqlModeMigrate && *baseline == 0 {
				return fmt.Errorf("--mode migrate 需要 --dir <migrations-dir>（或在已运行 doops sql --mode init 的项目中省略 --dir 默认读取 .dosql/migrations）或 --baseline <version>")
			}
			if *baseline > 0 {
				arguments["sql_baseline"] = *baseline
			}
			if *resume {
				arguments["sql_resume"] = true
			}
		}
		if parsedMode == sqlModeLint {
			stmt, err := resolveSQLText(*sqlText, *sqlFile, "--sql", "--file")
			if err != nil {
				return err
			}
			if stmt == "" {
				return fmt.Errorf("--mode lint 需要 --sql 或 --file")
			}
			arguments["sql"] = stmt
		}
		if err := attachSQLGovernanceArgs(arguments, *lockTimeout, *statementTimeout, *releaseTag, *policyFile, []string(overrideRules), *overrideReason); err != nil {
			return err
		}
		op := "ask"
		if parsedMode == sqlModeMigrate || parsedMode == sqlModeBackfill {
			op = "apply"
		}
		return runSkillPromptWith("dosql", op, *target, sessionName,
			fmt.Sprintf("DoSql %s", parsedMode), "auto", nil,
			arguments, servers, configErr, ss, verbose)
	}
	direct, err := buildSQLDirectInput(parsedMode, *sqlText, *sqlFile, *verify, *verifyFile, *database, *dryRun)
	if err != nil {
		return err
	}
	if direct != nil {
		if strings.TrimSpace(*request) != "" {
			return fmt.Errorf("--sql/--file 与 --request 只能二选一")
		}
		arguments := direct.promptArguments()
		if selectedCredential != "" {
			arguments["sql_credential"] = selectedCredential
		}
		if err := attachSQLGovernanceArgs(arguments, *lockTimeout, *statementTimeout, *releaseTag, *policyFile, []string(overrideRules), *overrideReason); err != nil {
			return err
		}
		return runSkillPromptWith("dosql", sqlGatewayOperation(parsedMode), *target, sessionName,
			sqlDirectAuditDescription(parsedMode, direct), "auto", nil,
			arguments, servers, configErr, ss, verbose)
	}
	if selectedCredential != "" {
		return fmt.Errorf("%s", withNext("--credential only applies to --sql/--file",
			"doops guide database   # choose query or apply within the authorized scope; use --sql/--file with --credential "+selectedCredential))
	}
	if parsedMode == sqlModeApply {
		// The model-driven apply path needed a signed approval nobody issues; a
		// change is always the verbatim, journaled --sql/--file path.
		return fmt.Errorf("%s", withNext("--mode apply runs SQL you wrote, verbatim; it does not translate --request",
			"doops sql --target "+*target+" --mode plan --request '<change>'   # prepare SQL within the user's existing authorization",
			"doops guide database   # check engine/statement rollback limits before apply",
			"doops sql --target "+*target+" --mode apply --sql '<sql>' --verify '<select>'   # add --dry-run only for rollback-capable batches; MySQL/Oracle implicit-commit DDL must run separately from DML"))
	}
	if parsedMode == sqlModeDiscover && strings.TrimSpace(*request) == "" {
		*request = "列出当前目标上的数据库实例。"
	}
	if strings.TrimSpace(*request) == "" {
		return fmt.Errorf("%s", withNext("sql needs --sql, --file or --request", "doops sql --target "+*target+" --mode query --sql 'select ...'", "doops guide database"))
	}
	if parsedMode == sqlModeDiscover && strings.TrimSpace(*root) != "" {
		return runLocalSQLResourceDiscovery(*root, *resourcesFile, *displayResourcesFile, *project, *environment, *script, *target, servers, configErr)
	}
	instruction, err := appendConversationReferences(buildSQLInstruction(parsedMode, *database, *request), []string(databaseRefs), []string(repositoryRefs))
	if err != nil {
		return err
	}
	credentialMode := ""
	if parsedMode == sqlModeQuery {
		credentialMode = "auto"
	}
	return runSkillPrompt("dosql", sqlGatewayOperation(parsedMode), *target, sessionName, instruction, credentialMode, nil, servers, configErr, ss, verbose)
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
		return errTargetNotFound(servers, target)
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
	case sqlModeInit:
		return sqlModeInit, nil
	case sqlModeQuery:
		return sqlModeQuery, nil
	case sqlModePlan:
		return sqlModePlan, nil
	case sqlModeApply:
		return sqlModeApply, nil
	case sqlModeDiscover:
		return sqlModeDiscover, nil
	case sqlModeInspect:
		return sqlModeInspect, nil
	case sqlModeVersions:
		return sqlModeVersions, nil
	case sqlModeHistory:
		return sqlModeHistory, nil
	case sqlModeLint:
		return sqlModeLint, nil
	case sqlModeDiff:
		return sqlModeDiff, nil
	case sqlModeDataDiff:
		return sqlModeDataDiff, nil
	case sqlModeStatus:
		return sqlModeStatus, nil
	case sqlModeMigrate:
		return sqlModeMigrate, nil
	case sqlModeBackfill:
		return sqlModeBackfill, nil
	case sqlModePack:
		return sqlModePack, nil
	default:
		return "", fmt.Errorf("unsupported --mode %q; use init, query, plan, apply, discover, inspect, diff, datadiff, status, migrate, backfill, pack, versions, history, or lint", value)
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
	}
	hint := ""
	if strings.TrimSpace(databaseHint) != "" {
		hint = "\n数据库提示：" + strings.TrimSpace(databaseHint)
	}
	return strings.TrimSpace(fmt.Sprintf(
		"使用 DoSql skill 处理当前 DoOps Gateway target。%s%s\n不要读取或索取密码、连接串或 token；不要绕过 DoSql CLI、journal、evidence 和 Gateway 授权。\n"+
			"查询前先用 mcp_dosql_read 的 discover 获取候选；只能使用本次返回的 database_asset_id，不要猜测、复用其它集群的 id 或凭据；候选的 engine 必须与 credential 匹配。同一错误最多重试一次，第二次仍失败就停止并报告候选，不要循环调用工具。\n"+
			"用户需求：%s",
		purpose, hint, strings.TrimSpace(request),
	))
}
