package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/internal/dosql/backfill"
	"github.com/user/doops/agent/internal/dosql/datadiff"
	"github.com/user/doops/agent/internal/dosql/diff"
	"github.com/user/doops/agent/internal/dosql/inspect"
	"github.com/user/doops/agent/internal/dosql/lexer"
	"github.com/user/doops/agent/internal/dosql/migrate"
	"github.com/user/doops/agent/internal/dosql/policy"
	"github.com/user/doops/agent/internal/dosql/recorder"
)

type doSQLDirectReport struct {
	OperationID          string            `json:"operation_id"`
	Mode                 string            `json:"mode"`
	Engine               string            `json:"engine"`
	Database             string            `json:"database"`
	Credential           string            `json:"credential"`
	StatementFingerprint string            `json:"statement_fingerprint"`
	Version              int64             `json:"version,omitempty"`
	VersionRecordError   string            `json:"version_record_error,omitempty"`
	JournalPath          string            `json:"journal_path,omitempty"`
	EvidencePath         string            `json:"evidence_path,omitempty"`
	SnapshotPath         string            `json:"snapshot_path,omitempty"`
	SchemaHash           string            `json:"schema_hash,omitempty"`
	Result               doSQLDirectResult `json:"result"`
}

func doSQLVersionSuffix(report doSQLDirectReport) string {
	if report.Version == 0 {
		return ""
	}
	suffix := fmt.Sprintf(" version=%d", report.Version)
	if report.VersionRecordError != "" {
		suffix += " version_record_error=" + report.VersionRecordError
	}
	return suffix
}

func doSQLDirectRequested(args api.AgentPromptParams) bool {
	return strings.TrimSpace(args.Skill) == "dosql" &&
		(strings.TrimSpace(args.SQL) != "" || strings.TrimSpace(args.SQLAction) != "")
}

func parseDoSQLDurationOption(raw string, def, max time.Duration, name string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < time.Second || d > max {
		return 0, fmt.Errorf("dosql_timeout_invalid: %s must be between 1s and %s (got %q)", name, max, raw)
	}
	return d, nil
}

func resolveDoSQLPolicy(args api.AgentPromptParams) (policy.Policy, error) {
	if strings.TrimSpace(args.SQLPolicy) != "" {
		return policy.ParseYAML([]byte(args.SQLPolicy))
	}
	return policy.Default(), nil
}

func doSQLRecorderStore(target doSQLTarget) (*recorder.Store, error) {
	root, _, err := doSQLStateRoot()
	if err != nil {
		return nil, err
	}
	return recorder.NewStore(root, target.Engine, target.Address, target.Database), nil
}

func doSQLSnapshotStore(target doSQLTarget) (*inspect.SnapshotStore, error) {
	root, _, err := doSQLStateRoot()
	if err != nil {
		return nil, err
	}
	return inspect.NewSnapshotStore(root, target.Engine, target.Address, target.Database), nil
}

func captureDoSQLSnapshot(ctx context.Context, target doSQLTarget, version int64) (string, string) {
	if target.Engine != "mysql" && target.Engine != "postgres" {
		return "", ""
	}
	db, err := openDoSQLDirectDB(doSQLDirectRequest{Target: target, ReadOnly: true})
	if err != nil {
		return "", ""
	}
	defer db.Close()
	ir, err := inspect.InspectSQL(ctx, db, target.Engine, target.Database, inspect.Options{})
	if err != nil {
		return "", ""
	}
	store, err := doSQLSnapshotStore(target)
	if err != nil {
		return "", ir.SchemaHash
	}
	path, err := store.Save(version, ir)
	if err != nil {
		return "", ir.SchemaHash
	}
	return path, ir.SchemaHash
}

func loadSchemaRef(ctx context.Context, target doSQLTarget, ref string, tables []string) (*inspect.SchemaIR, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" || ref == "live" {
		db, err := openDoSQLDirectDB(doSQLDirectRequest{Target: target, ReadOnly: true})
		if err != nil {
			return nil, doSQLFailure("dosql_connect_failed", err.Error())
		}
		defer db.Close()
		return inspect.InspectSQL(ctx, db, target.Engine, target.Database, inspect.Options{Tables: tables})
	}
	store, err := doSQLSnapshotStore(target)
	if err != nil {
		return nil, err
	}
	if ref == "latest" {
		return store.LoadLatest()
	}
	var ver int64
	if _, err := fmt.Sscanf(strings.TrimPrefix(ref, "v"), "%d", &ver); err != nil || ver <= 0 {
		return nil, fmt.Errorf("dosql_diff_ref_invalid: %q (use live, latest, or v<number>)", ref)
	}
	return store.Load(ver)
}

func handleDirectDoSQLAction(ctx context.Context, sessionID string, args api.AgentPromptParams, binding api.AgentCredentialBinding, target doSQLTarget) (string, error) {
	action := strings.ToLower(strings.TrimSpace(args.SQLAction))
	switch action {
	case "versions":
		journalPath, err := doSQLVersionJournalPath(target)
		if err != nil {
			return "", err
		}
		versions, err := listDoSQLVersions(journalPath)
		if err != nil {
			return "", err
		}
		snapStore, err := doSQLSnapshotStore(target)
		if err != nil {
			return "", err
		}
		snaps, _ := snapStore.ListVersions()
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mode": "versions", "engine": target.Engine, "database": target.Database,
			"credential": binding.Name, "versions": versions, "snapshots": snaps,
		}, "", "  ")
		return string(out), nil

	case "history":
		recStore, err := doSQLRecorderStore(target)
		if err != nil {
			return "", err
		}
		filterSession := strings.TrimSpace(sessionID)
		if args.SQLAllSessions {
			filterSession = ""
		}
		records, err := recStore.List(recorder.QueryFilter{
			SessionID: filterSession,
			Limit:     2000,
		})
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mode": "history", "engine": target.Engine, "database": target.Database,
			"credential": binding.Name, "records": records,
		}, "", "  ")
		return string(out), nil

	case "inspect":
		db, err := openDoSQLDirectDB(doSQLDirectRequest{Target: target, ReadOnly: true})
		if err != nil {
			return "", doSQLFailure("dosql_connect_failed", err.Error())
		}
		defer db.Close()
		ir, err := inspect.InspectSQL(ctx, db, target.Engine, target.Database, inspect.Options{Tables: args.SQLTables})
		if err != nil {
			return "", doSQLFailure("dosql_inspect_failed", err.Error())
		}
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mode": "inspect", "credential": binding.Name, "schema": ir,
		}, "", "  ")
		return string(out), nil

	case "datadiff":
		srcDBName := strings.TrimSpace(args.SQLDataDiffSource)
		if srcDBName == "" {
			return "", errors.New("dosql_datadiff_source_required: pass --source <source-database>")
		}
		table := strings.TrimSpace(args.SQLDataDiffTable)
		if table == "" {
			return "", errors.New("dosql_datadiff_table_required: pass --table <table-name>")
		}
		keyCol := strings.TrimSpace(args.SQLDataDiffKey)
		if keyCol == "" {
			keyCol = "id"
		}
		srcCandidate, err := resolveDirectDoSQLBinding(ctx, args.CredentialBindings, srcDBName, readDoSQLSecretData)
		if err != nil {
			return "", err
		}
		if srcCandidate.Engine != target.Engine {
			return "", fmt.Errorf("dosql_datadiff_engine_mismatch: source is %s, destination is %s", srcCandidate.Engine, target.Engine)
		}
		srcTarget, err := doSQLConnectionTarget(srcCandidate.Engine, srcCandidate.Database, srcCandidate.Data)
		if err != nil {
			return "", err
		}
		srcDB, err := openDoSQLDirectDB(doSQLDirectRequest{Target: srcTarget, ReadOnly: true})
		if err != nil {
			return "", doSQLFailure("dosql_connect_failed", err.Error())
		}
		defer srcDB.Close()
		dstDB, err := openDoSQLDirectDB(doSQLDirectRequest{Target: target, ReadOnly: true})
		if err != nil {
			return "", doSQLFailure("dosql_connect_failed", err.Error())
		}
		defer dstDB.Close()
		cols := args.SQLDataDiffColumns
		if len(cols) == 0 && (target.Engine == "mysql" || target.Engine == "postgres") {
			ir, inspectErr := inspect.InspectSQL(ctx, srcDB, target.Engine, srcTarget.Database, inspect.Options{Tables: []string{table}})
			if inspectErr == nil && len(ir.Tables) == 1 {
				for _, c := range ir.Tables[0].Columns {
					cols = append(cols, c.Name)
				}
			}
		}
		ddRes, err := datadiff.CompareIntKeyTables(ctx, srcDB, dstDB, datadiff.Options{
			Engine:      target.Engine,
			Table:       table,
			KeyColumn:   keyCol,
			Columns:     cols,
			ChunkSize:   args.SQLDataDiffChunk,
			GenerateFix: args.SQLDataDiffFix,
		})
		if err != nil {
			return "", doSQLFailure("dosql_datadiff_failed", err.Error())
		}
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mode": "datadiff", "source": srcTarget.Database, "target": target.Database,
			"credential": binding.Name, "datadiff": ddRes,
		}, "", "  ")
		return string(out), nil

	case "diff":
		fromRef := strings.TrimSpace(args.SQLDiffFrom)
		if fromRef == "" {
			fromRef = "latest"
		}
		toRef := strings.TrimSpace(args.SQLDiffTo)
		if toRef == "" {
			toRef = "live"
		}
		fromIR, err := loadSchemaRef(ctx, target, fromRef, args.SQLTables)
		if err != nil {
			return "", doSQLFailure("dosql_diff_source_unavailable", err.Error())
		}
		toIR, err := loadSchemaRef(ctx, target, toRef, args.SQLTables)
		if err != nil {
			return "", doSQLFailure("dosql_diff_target_unavailable", err.Error())
		}
		d := diff.CompareSchemas(fromIR, toIR, args.SQLTables)
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mode": "diff", "from": fromRef, "to": toRef, "credential": binding.Name, "diff": d,
		}, "", "  ")
		return string(out), nil

	case "status":
		var files []migrate.MigrationFile
		if strings.TrimSpace(args.SQLMigrations) != "" {
			if err := json.Unmarshal([]byte(args.SQLMigrations), &files); err != nil {
				return "", fmt.Errorf("dosql_migrate_manifest_invalid: %w", err)
			}
		}
		db, err := openDoSQLDirectDB(doSQLDirectRequest{Target: target, ReadOnly: true})
		if err != nil {
			return "", doSQLFailure("dosql_connect_failed", err.Error())
		}
		defer db.Close()
		store := &migrate.SQLHistoryStore{DB: db, Engine: target.Engine}
		applied, err := store.ListRevisions(ctx)
		if err != nil {
			return "", doSQLFailure("dosql_status_failed", err.Error())
		}
		status, err := migrate.ComputeStatus(applied, files)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mode": "status", "engine": target.Engine, "database": target.Database,
			"credential": binding.Name, "status": status,
		}, "", "  ")
		return string(out), nil

	case "migrate":
		recStore, err := doSQLRecorderStore(target)
		if err != nil {
			return "", err
		}
		pol, err := resolveDoSQLPolicy(args)
		if err != nil {
			return "", err
		}
		if len(args.SQLOverrideRules) > 0 && strings.TrimSpace(args.SQLOverrideReason) == "" {
			return "", errors.New("dosql_policy_override_reason_required: --override-rule requires --override-reason")
		}
		useJournal := !(target.Engine == "sqlite" && strings.TrimSpace(target.Address) == "")
		var journalPath string
		if useJournal {
			journalPath, err = doSQLVersionJournalPath(target)
			if err != nil {
				return "", err
			}
		}
		var files []migrate.MigrationFile
		if strings.TrimSpace(args.SQLMigrations) != "" {
			if err := json.Unmarshal([]byte(args.SQLMigrations), &files); err != nil {
				return "", fmt.Errorf("dosql_migrate_manifest_invalid: %w", err)
			}
		}
		db, err := openDoSQLDirectDB(doSQLDirectRequest{Target: target, ReadOnly: false})
		if err != nil {
			return "", doSQLFailure("dosql_connect_failed", err.Error())
		}
		defer db.Close()
		store := &migrate.SQLHistoryStore{DB: db, Engine: target.Engine}
		// Pre-flight Policy-as-Code check on every pending/resumable SQL migration
		// BEFORE migrate.Apply writes any running/failed row into _dosql_schema_history.
		if target.Engine != "mongodb" && len(files) > 0 {
			existingRevs, listErr := store.ListRevisions(ctx)
			if listErr != nil {
				return "", doSQLFailure("dosql_migrate_preflight_failed", listErr.Error())
			}
			checkApplied := existingRevs
			if args.SQLBaseline > 0 && len(existingRevs) == 0 {
				checkApplied = append(checkApplied, migrate.Revision{
					Version: args.SQLBaseline, Type: "baseline", Status: "succeeded",
				})
			}
			st, stErr := migrate.ComputeStatus(checkApplied, files)
			if stErr != nil {
				return "", stErr
			}
			toValidate := make([]migrate.MigrationFile, 0, len(st.Pending)+1)
			if st.Dirty != nil && args.SQLResume {
				for _, f := range files {
					if f.Version == st.Dirty.Version {
						toValidate = append(toValidate, f)
						break
					}
				}
			}
			toValidate = append(toValidate, st.Pending...)
			for _, f := range toValidate {
				fileArgs := args
				fileArgs.Operation = "apply"
				fileArgs.SQLAction = ""
				fileArgs.SQL = f.Content
				req, planErr := planDirectDoSQL(target.Engine, fileArgs)
				if planErr != nil {
					return "", planErr
				}
				if len(req.ParsedStatements) > 0 {
					polReport := policy.Evaluate(target.Engine, req.ParsedStatements, pol, args.SQLOverrideRules)
					if !polReport.Passed {
						var reasons []string
						for _, finding := range polReport.Findings {
							if finding.Severity == policy.SeverityError && !finding.Overridden {
								reasons = append(reasons, fmt.Sprintf("[%s] %s stmt#%d: %s", finding.RuleID, f.FileName, finding.StatementIndex, finding.Message))
							}
						}
						return "", fmt.Errorf("dosql_policy_rejected: %s", strings.Join(reasons, "; "))
					}
				}
			}
		}
		opID := "dbop_" + randomHex(8)
		var lastJournalVersion int64
		res, err := migrate.Apply(ctx, store, files, args.SQLBaseline, strings.TrimSpace(args.SQLReleaseTag), args.SQLResume,
			func(execCtx context.Context, f migrate.MigrationFile, startIdx int) (int, int, error) {
				fileArgs := args
				fileArgs.Operation = "apply"
				fileArgs.SQLAction = ""
				fileArgs.SQL = f.Content
				req, planErr := planDirectDoSQL(target.Engine, fileArgs)
				if planErr != nil {
					return 0, 0, planErr
				}
				if target.Engine != "mongodb" && len(req.ParsedStatements) > 0 {
					polReport := policy.Evaluate(target.Engine, req.ParsedStatements, pol, args.SQLOverrideRules)
					if !polReport.Passed {
						var reasons []string
						for _, finding := range polReport.Findings {
							if finding.Severity == policy.SeverityError && !finding.Overridden {
								reasons = append(reasons, fmt.Sprintf("[%s] %s stmt#%d: %s", finding.RuleID, f.FileName, finding.StatementIndex, finding.Message))
							}
						}
						return 0, len(req.Statements), fmt.Errorf("dosql_policy_rejected: %s", strings.Join(reasons, "; "))
					}
				}
				total := len(req.Statements)
				if startIdx > 0 {
					if startIdx >= total {
						return 0, total, nil
					}
					req.Statements = req.Statements[startIdx:]
					if len(req.ParsedStatements) >= total {
						req.ParsedStatements = req.ParsedStatements[startIdx:]
					}
				}
				req.Target = target
				var fileVersion int64
				fp := "sha256:" + doSQLStatementFingerprint(req.Statements)
				fileOpID := fmt.Sprintf("%s_v%d", opID, f.Version)
				if useJournal {
					ver, verErr := beginDoSQLVersion(journalPath, fileOpID, binding.Name, fp, req.Statements)
					if verErr != nil {
						return 0, total, verErr
					}
					fileVersion = ver
					lastJournalVersion = ver
				}
				recVersion := fileVersion
				if recVersion == 0 {
					recVersion = f.Version
				}
				req.OnStatement = func(rec recorder.StatementRecord) {
					rec.SessionID = sessionID
					rec.OperationID = fileOpID
					rec.Version = recVersion
					rec.Credential = binding.Name
					rec.ReleaseTag = strings.TrimSpace(args.SQLReleaseTag)
					rec.Mode = "migrate"
					rec.Statement = sanitizeSecretText(rec.Statement)
					rec.Error = sanitizeSecretText(rec.Error)
					_ = recStore.Append(rec)
				}
				execRes, execErr := executeDoSQLDirect(execCtx, req)
				if useJournal && fileVersion != 0 {
					_ = endDoSQLVersion(journalPath, fileVersion, fileOpID, binding.Name, fp, execRes, execErr)
				}
				return execRes.Applied, total, execErr
			})
		if err != nil {
			_ = writeDirectDoSQLEvidence(sessionID, doSQLDirectReport{
				OperationID: opID, Mode: "migrate", Engine: target.Engine, Database: target.Database,
				Credential: binding.Name, Version: lastJournalVersion, JournalPath: journalPath,
			}, err)
			return "", err
		}
		var snapPath, snapHash string
		if len(res.Executed) > 0 {
			snapVer := lastJournalVersion
			if snapVer == 0 {
				snapVer = res.Current
			}
			snapPath, snapHash = captureDoSQLSnapshot(ctx, target, snapVer)
		}
		evidencePath := writeDirectDoSQLEvidence(sessionID, doSQLDirectReport{
			OperationID: opID, Mode: "migrate", Engine: target.Engine, Database: target.Database,
			Credential: binding.Name, Version: lastJournalVersion, JournalPath: journalPath,
			SnapshotPath: snapPath, SchemaHash: snapHash,
		}, nil)
		out, _ := json.MarshalIndent(map[string]interface{}{
			"operation_id": opID, "mode": "migrate", "engine": target.Engine, "database": target.Database,
			"credential": binding.Name, "version": lastJournalVersion, "journal_path": journalPath,
			"evidence_path": evidencePath, "result": res, "snapshot_path": snapPath, "schema_hash": snapHash,
		}, "", "  ")
		return string(out), nil

	case "backfill":
		recStore, err := doSQLRecorderStore(target)
		if err != nil {
			return "", err
		}
		var throttle time.Duration
		if raw := strings.TrimSpace(args.SQLBackfillThrottle); raw != "" {
			var parseErr error
			throttle, parseErr = time.ParseDuration(raw)
			if parseErr != nil || throttle < 0 || throttle > time.Minute {
				return "", fmt.Errorf("dosql_backfill_throttle_invalid: %q", raw)
			}
		}
		db, err := openDoSQLDirectDB(doSQLDirectRequest{Target: target, ReadOnly: false})
		if err != nil {
			return "", doSQLFailure("dosql_connect_failed", err.Error())
		}
		defer db.Close()
		opID := "dbop_" + randomHex(8)
		bfRes, err := backfill.RunIntKeyBackfill(ctx, db, backfill.Options{
			Engine:       target.Engine,
			Table:        strings.TrimSpace(args.SQLBackfillTable),
			KeyColumn:    strings.TrimSpace(args.SQLBackfillKey),
			SetClause:    strings.TrimSpace(args.SQLBackfillSet),
			WhereClause:  strings.TrimSpace(args.SQLBackfillWhere),
			BatchSize:    args.SQLBackfillBatch,
			Throttle:     throttle,
			StartAfterID: args.SQLBackfillStartID,
			DryRun:       args.SQLDryRun,
		})
		if err != nil {
			_ = writeDirectDoSQLEvidence(sessionID, doSQLDirectReport{
				OperationID: opID, Mode: "backfill", Engine: target.Engine, Database: target.Database, Credential: binding.Name,
			}, err)
			return "", err
		}
		bfMode := "backfill"
		if args.SQLDryRun {
			bfMode = "backfill_dry_run"
		}
		_ = recStore.Append(recorder.StatementRecord{
			SessionID:    sessionID,
			OperationID:  opID,
			Engine:       target.Engine,
			Database:     target.Database,
			Credential:   binding.Name,
			ReleaseTag:   strings.TrimSpace(args.SQLReleaseTag),
			Mode:         bfMode,
			Kind:         "backfill",
			Statement:    sanitizeSecretText(fmt.Sprintf("BACKFILL %s SET %s WHERE %s", bfRes.Table, strings.TrimSpace(args.SQLBackfillSet), strings.TrimSpace(args.SQLBackfillWhere))),
			AffectedRows: bfRes.TotalAffected,
			DurationMS:   bfRes.DurationMS,
			Status:       "succeeded",
		})
		evidencePath := writeDirectDoSQLEvidence(sessionID, doSQLDirectReport{
			OperationID: opID, Mode: bfMode, Engine: target.Engine, Database: target.Database, Credential: binding.Name,
		}, nil)
		out, _ := json.MarshalIndent(map[string]interface{}{
			"operation_id": opID, "mode": bfMode, "engine": target.Engine, "database": target.Database,
			"credential": binding.Name, "evidence_path": evidencePath, "result": bfRes,
		}, "", "  ")
		return string(out), nil

	case "lint":
		if strings.TrimSpace(args.SQL) == "" {
			return "", errors.New("dosql_statement_required")
		}
		parsed, err := lexer.Split(target.Engine, args.SQL)
		if err != nil {
			return "", err
		}
		pol, err := resolveDoSQLPolicy(args)
		if err != nil {
			return "", err
		}
		rep := policy.Evaluate(target.Engine, parsed, pol, args.SQLOverrideRules)
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mode": "lint", "engine": target.Engine, "database": target.Database,
			"statements": len(parsed), "segments": lexer.PlanSegments(parsed), "policy": rep,
		}, "", "  ")
		return string(out), nil

	default:
		return "", fmt.Errorf("dosql_action_unsupported: %q (use versions, history, inspect, diff, datadiff, status, migrate, backfill, or lint)", action)
	}
}

func handleDirectDoSQLPrompt(ctx context.Context, sessionID string, args api.AgentPromptParams) (string, error) {
	candidate, err := resolveDirectDoSQLBinding(ctx, args.CredentialBindings, args.SQLDatabase, readDoSQLSecretData)
	if err != nil {
		return "", err
	}
	binding, data, engine, database := candidate.Binding, candidate.Data, candidate.Engine, candidate.Database
	target, err := doSQLConnectionTarget(engine, database, data)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.SQLAction) != "" {
		return handleDirectDoSQLAction(ctx, sessionID, args, binding, target)
	}
	request, err := planDirectDoSQL(engine, args)
	if err != nil {
		return "", err
	}
	request.Target = target
	report := doSQLDirectReport{
		OperationID: "dbop_" + randomHex(8), Mode: directDoSQLMode(request), Engine: engine,
		Database: database, Credential: binding.Name,
		StatementFingerprint: "sha256:" + doSQLStatementFingerprint(request.Statements),
	}
	// Policy-as-Code enforcement for mutating SQL batches (unless overridden with audit reason).
	if !request.ReadOnly && engine != "mongodb" && len(request.ParsedStatements) > 0 {
		pol, err := resolveDoSQLPolicy(args)
		if err != nil {
			return "", err
		}
		if len(args.SQLOverrideRules) > 0 && strings.TrimSpace(args.SQLOverrideReason) == "" {
			return "", errors.New("dosql_policy_override_reason_required: --override-rule requires --override-reason")
		}
		polReport := policy.Evaluate(engine, request.ParsedStatements, pol, args.SQLOverrideRules)
		request.PolicyReport = &polReport
		if !polReport.Passed {
			var reasons []string
			for _, f := range polReport.Findings {
				if f.Severity == policy.SeverityError && !f.Overridden {
					reasons = append(reasons, fmt.Sprintf("[%s] stmt#%d: %s", f.RuleID, f.StatementIndex, f.Message))
				}
			}
			return "", fmt.Errorf("dosql_policy_rejected: %s", strings.Join(reasons, "; "))
		}
	}
	// A real apply takes its version before touching the database; if the
	// journal cannot record it, nothing runs. Queries and dry-runs are not versions.
	var journalPath string
	if !request.ReadOnly && !request.DryRun {
		journalPath, err = doSQLVersionJournalPath(target)
		if err != nil {
			return "", fmt.Errorf("%w (operation_id=%s): nothing was executed", err, report.OperationID)
		}
		version, err := beginDoSQLVersion(journalPath, report.OperationID, binding.Name, report.StatementFingerprint, request.Statements)
		if err != nil {
			return "", fmt.Errorf("%w (operation_id=%s): nothing was executed", err, report.OperationID)
		}
		report.Version = version
		report.JournalPath = journalPath
	}
	// Automatic statement recording hook (best-effort persistent history for audit & pack).
	recStore, err := doSQLRecorderStore(target)
	if err != nil {
		return "", err
	}
	request.OnStatement = func(rec recorder.StatementRecord) {
		rec.SessionID = sessionID
		rec.OperationID = report.OperationID
		rec.Version = report.Version
		rec.Credential = binding.Name
		rec.ReleaseTag = strings.TrimSpace(args.SQLReleaseTag)
		rec.Mode = report.Mode
		rec.Statement = sanitizeSecretText(rec.Statement)
		rec.Error = sanitizeSecretText(rec.Error)
		_ = recStore.Append(rec)
	}
	result, execErr := executeDoSQLDirect(ctx, request)
	report.Result = result
	if report.Version != 0 {
		if err := endDoSQLVersion(journalPath, report.Version, report.OperationID, binding.Name, report.StatementFingerprint, result, execErr); err != nil {
			// The change already ran; say the record is missing instead of hiding it.
			report.VersionRecordError = err.Error()
		}
		if execErr == nil {
			report.SnapshotPath, report.SchemaHash = captureDoSQLSnapshot(ctx, target, report.Version)
		}
	}
	report.EvidencePath = writeDirectDoSQLEvidence(sessionID, report, execErr)
	if execErr != nil {
		if ctx.Err() != nil && result.ImplicitCommits > 0 {
			return "", fmt.Errorf("%w: %s (operation_id=%s)", errExecutionOutcomeUnknown, strings.TrimSpace(partialCommitNote(&result)), report.OperationID)
		}
		return "", fmt.Errorf("%w (operation_id=%s%s)", execErr, report.OperationID, doSQLVersionSuffix(report))
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", errors.New("dosql_result_encode_failed")
	}
	return string(encoded), nil
}

// planDirectDoSQL turns the verbatim SQL text into an executable request; a
// query prompt only accepts read statements so ask authority never mutates.
func planDirectDoSQL(engine string, args api.AgentPromptParams) (doSQLDirectRequest, error) {
	apply := strings.ToLower(strings.TrimSpace(args.Operation)) == "apply"
	lockTimeout, err := parseDoSQLDurationOption(args.SQLLockTimeout, doSQLDirectLockWait, doSQLMaxLockWait, "--lock-timeout")
	if err != nil {
		return doSQLDirectRequest{}, err
	}
	stmtTimeout, err := parseDoSQLDurationOption(args.SQLStatementTimeout, doSQLDirectStatementT, doSQLMaxStatementT, "--statement-timeout")
	if err != nil {
		return doSQLDirectRequest{}, err
	}
	var statements []string
	var parsed []lexer.Statement
	if engine == "mongodb" {
		statements, err = parseMongoStatements(args.SQL)
	} else {
		parsed, err = lexer.Split(engine, args.SQL)
		if err == nil {
			statements = make([]string, len(parsed))
			for i, s := range parsed {
				statements[i] = s.Executable
			}
		}
	}
	if err != nil {
		return doSQLDirectRequest{}, err
	}
	request := doSQLDirectRequest{
		Statements:       statements,
		ParsedStatements: parsed,
		ReadOnly:         !apply,
		DryRun:           apply && args.SQLDryRun,
		LockTimeout:      lockTimeout,
		StatementTimeout: stmtTimeout,
	}
	if !apply {
		if args.SQLDryRun || strings.TrimSpace(args.SQLVerification) != "" {
			return doSQLDirectRequest{}, errors.New("dosql_query_options_invalid: --verify and --dry-run require --mode apply")
		}
		for index, statement := range statements {
			if !directReadOnlyStatement(engine, statement) {
				return doSQLDirectRequest{}, fmt.Errorf("dosql_query_requires_read_only: statement %d is not a read statement; use --mode apply", index+1)
			}
		}
		return request, nil
	}
	if strings.TrimSpace(args.SQLVerification) != "" {
		verification, err := splitDirectStatements(engine, args.SQLVerification)
		if err != nil {
			return doSQLDirectRequest{}, err
		}
		if len(verification) != 1 || !directReadOnlyStatement(engine, verification[0]) {
			return doSQLDirectRequest{}, errors.New("dosql_verification_invalid: verification must be exactly one read statement")
		}
		request.Verification = verification[0]
	}
	return request, nil
}

func splitDirectStatements(engine, text string) ([]string, error) {
	if engine == "mongodb" {
		return parseMongoStatements(text)
	}
	return splitDoSQLStatements(engine, text)
}

func directReadOnlyStatement(engine, statement string) bool {
	if engine == "mongodb" {
		return mongoOperationKind(statement) == "read"
	}
	parsed, err := lexer.Split(engine, statement)
	if err != nil || len(parsed) != 1 {
		return false
	}
	return parsed[0].Kind == lexer.KindRead
}

func directDoSQLMode(request doSQLDirectRequest) string {
	switch {
	case request.ReadOnly:
		return "query"
	case request.DryRun:
		return "dry_run"
	default:
		return "apply"
	}
}

type doSQLBindingCandidate struct {
	Binding  api.AgentCredentialBinding
	Data     map[string]string
	Engine   string
	Database string
}

type doSQLSecretReader func(context.Context, api.AgentCredentialBinding) (map[string]string, error)

// resolveDirectDoSQLBinding picks the target's database binding without a
// caller-chosen credential: one binding is used as is, several are told apart
// by the database each credential points to. Errors carry names only.
func resolveDirectDoSQLBinding(ctx context.Context, bindings []api.AgentCredentialBinding, database string, read doSQLSecretReader) (doSQLBindingCandidate, error) {
	var databaseBindings []api.AgentCredentialBinding
	for _, binding := range bindings {
		if binding.Use == string(CredentialUseDatabase) {
			databaseBindings = append(databaseBindings, binding)
		}
	}
	if len(databaseBindings) == 0 {
		return doSQLBindingCandidate{}, errors.New("dosql_credential_missing: run credential configure --use database for this target")
	}
	database = strings.TrimSpace(database)
	var candidates []doSQLBindingCandidate
	var unusable []string
	for _, binding := range databaseBindings {
		candidate, err := loadDoSQLBindingCandidate(ctx, binding, read)
		if err != nil {
			if len(databaseBindings) == 1 {
				return doSQLBindingCandidate{}, err
			}
			// Keep the reason: one broken binding must not hide behind a generic miss.
			unusable = append(unusable, binding.Name+"("+doSQLErrorCategory(err)+")")
			continue
		}
		candidates = append(candidates, candidate)
	}
	if database == "" {
		if len(candidates) == 1 && len(unusable) == 0 {
			return candidates[0], nil
		}
		return doSQLBindingCandidate{}, fmt.Errorf("dosql_database_required: target binds %s (name=engine/database); pass --database <database>",
			describeDoSQLCandidates(candidates, unusable))
	}
	var matches []doSQLBindingCandidate
	for _, candidate := range candidates {
		if candidate.Database == database {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		// Users copy the credential/binding name shown by "credential list".
		// Accept it as an alias for the database name when it is unique, so a
		// valid listed binding does not fail as if it were unbound.
		for _, candidate := range candidates {
			if candidate.Binding.Name == database || candidate.Binding.ResourceName == database {
				matches = append(matches, candidate)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return doSQLBindingCandidate{}, fmt.Errorf("dosql_database_not_bound: no database credential on this target points to %q; target binds %s (name=engine/database); pass --database <database>",
			database, describeDoSQLCandidates(candidates, unusable))
	}
	names := make([]string, 0, len(matches))
	for _, candidate := range matches {
		names = append(names, candidate.Binding.Name)
	}
	sort.Strings(names)
	return doSQLBindingCandidate{}, fmt.Errorf("dosql_credential_ambiguous: credentials %s all point to %q; revoke the duplicate binding",
		strings.Join(names, ","), database)
}

func loadDoSQLBindingCandidate(ctx context.Context, binding api.AgentCredentialBinding, read doSQLSecretReader) (doSQLBindingCandidate, error) {
	data, err := read(ctx, binding)
	if err != nil {
		return doSQLBindingCandidate{}, err
	}
	engine, database, err := inferDoSQLEngine(data)
	if err != nil {
		return doSQLBindingCandidate{}, err
	}
	return doSQLBindingCandidate{Binding: binding, Data: data, Engine: engine, Database: database}, nil
}

func describeDoSQLCandidates(candidates []doSQLBindingCandidate, unusable []string) string {
	parts := make([]string, 0, len(candidates)+len(unusable))
	for _, candidate := range candidates {
		parts = append(parts, candidate.Binding.Name+"="+candidate.Engine+"/"+candidate.Database)
	}
	sort.Strings(parts)
	sort.Strings(unusable)
	if len(unusable) != 0 {
		parts = append(parts, "unusable: "+strings.Join(unusable, ","))
	}
	return strings.Join(parts, ", ")
}

// doSQLErrorCategory keeps only the stable category prefix of an Agent error.
func doSQLErrorCategory(err error) string {
	category, _, _ := strings.Cut(err.Error(), ":")
	return strings.TrimSpace(category)
}

func inferDoSQLEngine(data map[string]string) (string, string, error) {
	if raw := strings.TrimSpace(data["SPRING_DATASOURCE_URL"]); raw != "" {
		parsed, err := url.Parse("mysql://" + strings.TrimPrefix(raw, "jdbc:mysql://"))
		if err != nil || !strings.HasPrefix(raw, "jdbc:mysql://") {
			return "", "", errors.New("dosql_connection_uri_invalid: SPRING_DATASOURCE_URL must be jdbc:mysql://host:port/database")
		}
		return "mysql", strings.Trim(parsed.Path, "/"), nil
	}
	parsed, err := url.Parse(strings.TrimSpace(data["connection_uri"]))
	if err != nil || parsed.Scheme == "" {
		return "", "", errors.New("dosql_connection_uri_invalid: credential needs connection_uri or SPRING_DATASOURCE_URL")
	}
	database := strings.Trim(parsed.Path, "/")
	if engine, ok := doSQLEngineForScheme(parsed.Scheme); ok {
		return engine, database, nil
	}
	return "", "", fmt.Errorf("dosql_engine_unsupported: %s is not supported for direct SQL", parsed.Scheme)
}

// Evidence is best effort: a full disk must not turn a committed change into an error.
func writeDirectDoSQLEvidence(sessionID string, report doSQLDirectReport, execErr error) string {
	workspace, err := workspacePath(sessionID)
	if err != nil {
		return ""
	}
	directory := filepath.Join(workspace, ".dosql", "operations")
	if os.MkdirAll(directory, 0700) != nil {
		return ""
	}
	evidence := map[string]interface{}{
		"schema": "dosql.direct-evidence.v1", "recorded_at": time.Now().UTC().Format(time.RFC3339Nano), "report": report,
	}
	if execErr != nil {
		evidence["error"] = sanitizeSecretText(execErr.Error())
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return ""
	}
	path := filepath.Join(directory, report.OperationID+".evidence.json")
	if os.WriteFile(path, encoded, 0600) != nil {
		return ""
	}
	return path
}
