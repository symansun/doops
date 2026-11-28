package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type pluginConfigRecord struct {
	Configured  bool
	ScenarioID  string
	Revision    int64
	UpdatedBy   string
	UpdatedAtMs int64
}

type pluginRevisionConflictError struct {
	CurrentRevision int64
}

func (e *pluginRevisionConflictError) Error() string {
	return fmt.Sprintf("plugin_revision_conflict: current_revision=%d", e.CurrentRevision)
}

type pluginMutationParams struct {
	Action           string // apply_scenario | set_plugin | clear_override | reset
	ScenarioID       string
	PluginID         string
	Enabled          bool
	ExpectedRevision int64
	DryRun           bool
	Actor            string
}

func (s *GatewayStore) GetPluginConfig(ctx context.Context) (pluginConfigRecord, map[string]bool, error) {
	cfg := pluginConfigRecord{
		Configured: false,
		ScenarioID: defaultPluginScenarioID,
		Revision:   0,
	}
	var rowScenario, rowUpdatedBy string
	var rowRevision, rowUpdatedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT scenario_id, revision, updated_by, updated_at_ms FROM platform_plugin_config WHERE id = ?`,
		pluginConfigRowID,
	).Scan(&rowScenario, &rowRevision, &rowUpdatedBy, &rowUpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return cfg, nil, err
	}
	if err == nil {
		cfg.Configured = true
		cfg.ScenarioID = strings.TrimSpace(rowScenario)
		if cfg.ScenarioID == "" {
			cfg.ScenarioID = defaultPluginScenarioID
		}
		cfg.Revision = rowRevision
		cfg.UpdatedBy = rowUpdatedBy
		cfg.UpdatedAtMs = rowUpdatedAt
	}

	overrides, err := readPluginOverrides(ctx, s.db)
	if err != nil {
		return cfg, nil, err
	}
	return cfg, overrides, nil
}

type pluginRowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func readPluginOverrides(ctx context.Context, q pluginRowQuerier) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT plugin_id, enabled FROM platform_plugin_overrides ORDER BY plugin_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		var enabled int64
		if err := rows.Scan(&id, &enabled); err != nil {
			return nil, err
		}
		out[id] = enabled != 0
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// MutatePluginConfig applies (or previews when DryRun is true) a scenario
// switch, single-plugin override, stale override cleanup, or reset.
// It uses optimistic concurrency control via ExpectedRevision and never touches
// platform_skills, platform_mcp_servers, or monitor_schedules.
func (s *GatewayStore) MutatePluginConfig(ctx context.Context, params pluginMutationParams) (PluginMutationResponse, error) {
	var resp PluginMutationResponse

	schedules, err := s.ListMonitorSchedules(ctx)
	if err != nil {
		return resp, err
	}
	systemL2 := s.loadSystemL2Manifests(ctx)

	if params.DryRun {
		cfg, beforeOverrides, err := s.GetPluginConfig(ctx)
		if err != nil {
			return resp, err
		}
		if cfg.Revision != params.ExpectedRevision {
			return resp, &pluginRevisionConflictError{CurrentRevision: cfg.Revision}
		}
		afterScenario, afterOverrides, err := computePluginMutationTarget(cfg.ScenarioID, beforeOverrides, params, systemL2)
		if err != nil {
			return resp, err
		}
		diff := computePluginDiff(cfg.ScenarioID, beforeOverrides, afterScenario, afterOverrides, schedules, systemL2)
		return PluginMutationResponse{
			DryRun:     true,
			Revision:   cfg.Revision,
			ScenarioID: afterScenario,
			Customized: countActivePluginOverrides(afterScenario, afterOverrides, systemL2) > 0,
			Diff:       diff,
		}, nil
	}

	dialect := s.dialect
	if dialect == "" {
		dialect = gatewayDialect()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resp, err
	}
	defer tx.Rollback()

	// For MySQL, ensure the singleton config row exists inside the transaction
	// so SELECT ... FOR UPDATE serializes concurrent first-time writers. If the
	// revision check fails and we roll back, no row is left behind.
	if dialect == gatewayDialectMySQL {
		seed := gatewayUpsert{
			insert:    "INSERT INTO platform_plugin_config (id, scenario_id, revision, updated_by, updated_at_ms) VALUES (?, ?, ?, ?, ?)",
			target:    []string{"id"},
			doNothing: true,
		}
		if _, err := tx.ExecContext(ctx, seed.render(dialect), pluginConfigRowID, defaultPluginScenarioID, 0, "", 0); err != nil {
			return resp, err
		}
	}

	selectSQL := `SELECT scenario_id, revision, updated_by, updated_at_ms FROM platform_plugin_config WHERE id = ?`
	if dialect == gatewayDialectMySQL {
		selectSQL += ` FOR UPDATE`
	}

	cfg := pluginConfigRecord{
		ScenarioID: defaultPluginScenarioID,
		Revision:   0,
	}
	var rowScenario, rowUpdatedBy string
	var rowRevision, rowUpdatedAt int64
	err = tx.QueryRowContext(ctx, selectSQL, pluginConfigRowID).Scan(&rowScenario, &rowRevision, &rowUpdatedBy, &rowUpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return resp, err
	}
	if err == nil {
		cfg.Configured = true
		cfg.ScenarioID = strings.TrimSpace(rowScenario)
		if cfg.ScenarioID == "" {
			cfg.ScenarioID = defaultPluginScenarioID
		}
		cfg.Revision = rowRevision
		cfg.UpdatedBy = rowUpdatedBy
		cfg.UpdatedAtMs = rowUpdatedAt
	}

	if cfg.Revision != params.ExpectedRevision {
		return resp, &pluginRevisionConflictError{CurrentRevision: cfg.Revision}
	}

	beforeOverrides, err := readPluginOverrides(ctx, tx)
	if err != nil {
		return resp, err
	}

	afterScenario, afterOverrides, err := computePluginMutationTarget(cfg.ScenarioID, beforeOverrides, params, systemL2)
	if err != nil {
		return resp, err
	}

	nowMs := time.Now().UnixMilli()
	nextRev := cfg.Revision + 1

	switch params.Action {
	case "apply_scenario", "reset":
		if _, err := tx.ExecContext(ctx, `DELETE FROM platform_plugin_overrides`); err != nil {
			return resp, err
		}
	case "set_plugin":
		if val, keep := afterOverrides[params.PluginID]; keep {
			enabledInt := int64(0)
			if val {
				enabledInt = 1
			}
			upsert := gatewayUpsert{
				insert: "INSERT INTO platform_plugin_overrides (plugin_id, enabled, updated_by, updated_at_ms) VALUES (?, ?, ?, ?)",
				target: []string{"plugin_id"},
				set:    []string{"enabled=excluded.enabled", "updated_by=excluded.updated_by", "updated_at_ms=excluded.updated_at_ms"},
			}
			if _, err := tx.ExecContext(ctx, upsert.render(dialect), params.PluginID, enabledInt, params.Actor, nowMs); err != nil {
				return resp, err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `DELETE FROM platform_plugin_overrides WHERE plugin_id = ?`, params.PluginID); err != nil {
				return resp, err
			}
		}
	case "clear_override":
		if _, err := tx.ExecContext(ctx, `DELETE FROM platform_plugin_overrides WHERE plugin_id = ?`, params.PluginID); err != nil {
			return resp, err
		}
	default:
		return resp, fmt.Errorf("invalid_plugin_request")
	}

	upsertCfg := gatewayUpsert{
		insert: "INSERT INTO platform_plugin_config (id, scenario_id, revision, updated_by, updated_at_ms) VALUES (?, ?, ?, ?, ?)",
		target: []string{"id"},
		set:    []string{"scenario_id=excluded.scenario_id", "revision=excluded.revision", "updated_by=excluded.updated_by", "updated_at_ms=excluded.updated_at_ms"},
	}
	if _, err := tx.ExecContext(ctx, upsertCfg.render(dialect), pluginConfigRowID, afterScenario, nextRev, params.Actor, nowMs); err != nil {
		return resp, err
	}

	if err := tx.Commit(); err != nil {
		return resp, err
	}

	diff := computePluginDiff(cfg.ScenarioID, beforeOverrides, afterScenario, afterOverrides, schedules, systemL2)
	return PluginMutationResponse{
		DryRun:     false,
		Revision:   nextRev,
		ScenarioID: afterScenario,
		Customized: countActivePluginOverrides(afterScenario, afterOverrides, systemL2) > 0,
		Diff:       diff,
	}, nil
}

func (s *GatewayStore) loadSystemL2Manifests(ctx context.Context) map[string]*SkillPluginManifest {
	out := make(map[string]*SkillPluginManifest)
	if s == nil {
		return out
	}
	skills, err := s.ListPlatformSkills(ctx, TokenAuth{UserID: "system"}, "system", "", false)
	if err != nil {
		return out
	}
	for _, sk := range skills {
		if sk.Scope != "system" {
			continue
		}
		if m := decodeStoredSkillUIManifest(sk.UIManifestJSON); m != nil && strings.TrimSpace(sk.UIManifestError) == "" {
			out["skill."+sk.Slug] = m
		}
	}
	return out
}

func computePluginMutationTarget(
	beforeScenario string,
	beforeOverrides map[string]bool,
	params pluginMutationParams,
	systemL2 map[string]*SkillPluginManifest,
) (string, map[string]bool, error) {
	afterOverrides := cloneBoolMap(beforeOverrides)
	switch params.Action {
	case "apply_scenario":
		if _, ok := findPluginScenario(params.ScenarioID); !ok {
			return "", nil, errors.New("unknown_scenario")
		}
		return params.ScenarioID, map[string]bool{}, nil
	case "reset":
		return defaultPluginScenarioID, map[string]bool{}, nil
	case "set_plugin":
		if _, ok := findBuiltinPlugin(params.PluginID); ok {
			defaults := scenarioDefaultSet(beforeScenario)
			if params.Enabled == defaults[params.PluginID] {
				delete(afterOverrides, params.PluginID)
			} else {
				afterOverrides[params.PluginID] = params.Enabled
			}
			return beforeScenario, afterOverrides, nil
		}
		if m, ok := systemL2[params.PluginID]; ok && m != nil {
			def := skillPluginScenarioDefault(m, beforeScenario)
			if params.Enabled == def {
				delete(afterOverrides, params.PluginID)
			} else {
				afterOverrides[params.PluginID] = params.Enabled
			}
			return beforeScenario, afterOverrides, nil
		}
		return "", nil, errors.New("invalid_plugin_id")
	case "clear_override":
		if _, ok := beforeOverrides[params.PluginID]; !ok {
			return "", nil, errors.New("invalid_plugin_id")
		}
		delete(afterOverrides, params.PluginID)
		return beforeScenario, afterOverrides, nil
	default:
		return "", nil, errors.New("invalid_plugin_request")
	}
}

func countActivePluginOverrides(scenarioID string, overrides map[string]bool, systemL2 map[string]*SkillPluginManifest) int {
	defaults := scenarioDefaultSet(scenarioID)
	count := 0
	for _, p := range builtinPluginCatalog().Plugins {
		if v, ok := overrides[p.ID]; ok && v != defaults[p.ID] {
			count++
		}
	}
	for id, m := range systemL2 {
		if v, ok := overrides[id]; ok && v != skillPluginScenarioDefault(m, scenarioID) {
			count++
		}
	}
	return count
}

// cleanupSkillPluginOverride removes any orphaned skill.<slug> override when a
// Skill package is deleted. Kept inside plugin_store.go so platform_plugin_*
// table references stay strictly isolated in plugin files.
func (s *GatewayStore) cleanupSkillPluginOverride(ctx context.Context, slug string) {
	if s == nil || s.db == nil || slug == "" {
		return
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM platform_plugin_overrides WHERE plugin_id = ?`, "skill."+slug)
}
