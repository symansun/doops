package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Project only explicitly selected response columns, inside the same read snapshot.
func inspectionRows(tx *sql.Tx, query string, args ...any) ([]map[string]any, error) {
	rs, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rs.Next() {
		values := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range ptr {
			ptr[i] = &values[i]
		}
		if err := rs.Scan(ptr...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, k := range cols {
			row[k] = values[i]
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

const inspectionJoins = ` FROM inspection_items s JOIN inspection_runs r ON r.id=s.run_id JOIN inspection_plans p ON p.id=r.plan_id `

func inspectionStateAt(clock string) string {
	return `CASE WHEN s.status='pending' AND (s.deadline<=` + clock + ` OR ` + inspectBlock + `<>'') THEN 'missing' ELSE s.status END`
}

// inspectionReasonAt takes the database clock as a rendered expression so the
// dialect layer owns how "now" is spelled.
func inspectionReasonAt(clock string) string {
	return `CASE WHEN s.status='pending' AND ` + inspectBlock + `<>'' THEN ` + inspectBlock + ` WHEN s.status='pending' AND s.deadline<=` + clock + ` THEN 'collection_deadline_elapsed' ELSE s.reason END`
}

// inspectionWorkWindowSQL renders the "in flight right now" window of the work
// view. opened and deadline are integer epoch seconds, so the window is an
// integer comparison against the rendered database clock on both dialects.
func inspectionWorkWindowSQL(dialect gatewaySQLDialect) string {
	return inspectionWorkWindowAt(gatewayEpochNowFor(dialect))
}

// inspectionWorkWindowAt pins the clock so a test can prove the two dialect
// renderings agree on the open/closed boundary instead of on wall-clock time.
func inspectionWorkWindowAt(clock string) string {
	return `s.opened<=` + clock + ` AND s.deadline>` + clock
}

// inspectionOverdueSQL renders the overview's overdue marker: a due plan is
// overdue once a day has passed past its next_due epoch second.
func inspectionOverdueSQL(dialect gatewaySQLDialect) string {
	return inspectionOverdueAt(gatewayEpochNowFor(dialect))
}

// inspectionOverdueAt pins the clock for the same boundary proof as
// inspectionWorkWindowAt.
func inspectionOverdueAt(clock string) string {
	return `p.next_due+86400<=` + clock
}

// inspectStateSQL and inspectReasonSQL render the clock at call time. They used
// to be package-level vars evaluated during init, which baked the default
// SQLite spelling (unixepoch()) into the text; a Gateway started with
// DOOPS_GATEWAY_STORE=mysql then failed at execution with a missing function.
func inspectStateSQL() string  { return inspectionStateAt(gatewayEpochNowSQL()) }
func inspectReasonSQL() string { return inspectionReasonAt(gatewayEpochNowSQL()) }

func (s *GatewayStore) inspectionRead(r *http.Request, a TokenAuth) (any, error) {
	tx, err := s.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := r.URL.Query()
	for k, v := range q {
		if len(v) != 1 || (k != "view" && k != "cluster_id" && k != "run_id") {
			return nil, inspectErr(400, "unknown or duplicate query parameter")
		}
	}
	scope := q.Get("cluster_id")
	if scope != "" && !safeMachineID(scope) {
		return nil, inspectErr(400, "invalid cluster asset ID")
	}
	actor, err := resolveInventoryActor(r.Context(), tx, a)
	if err != nil {
		return nil, err
	}
	pred, predArgs := actor.targetPredicate("s.cluster", "s.instance")
	args := append(append([]any{}, predArgs...), scope, scope)
	filter := pred + ` AND (?='' OR s.cluster_id=?)`
	view := q.Get("view")
	if q.Has("run_id") && view != "report" && view != "download" {
		return nil, inspectErr(400, "run_id requires report or download view")
	}
	switch view {
	case "weekly":
		return inspectionWeekly(tx, a, scope, time.Now().UTC())
	case "work":
		rows, err := inspectionRows(tx, `SELECT s.id AS item_id,s.run_id,s.asset_id,s.cluster,s.instance,s.check_id,s.generation,s.opened,s.deadline `+inspectionJoins+` WHERE `+filter+` AND p.executor=? AND s.status='pending' AND `+inspectionWorkWindowSQL(gatewayDialect())+` AND `+inspectBlock+`='' ORDER BY s.deadline,s.id LIMIT 256`, append(args, a.UserID)...)
		return map[string]any{"work": rows}, err
	case "report", "download":
		id, err := strconv.ParseInt(q.Get("run_id"), 10, 64)
		if err != nil || id < 1 {
			return nil, inspectErr(400, "positive run_id required")
		}
		args = append(args, id)
		rows, err := inspectionRows(tx, `SELECT s.id AS item_id,s.asset_id,s.cluster,s.instance,s.cluster_id,s.check_id,s.generation,s.opened,s.deadline,`+inspectStateSQL()+` AS status,`+inspectReasonSQL()+` AS reason,s.repair,s.repair_by,s.repair_at `+inspectionJoins+` WHERE `+filter+` AND r.id=? ORDER BY s.id`, args...)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, inspectErr(404, "run not found")
		}
		history, err := inspectionRows(tx, `SELECT e.item_id,e.generation,e.actor,e.received,e.status,e.reason,e.evidence,e.digest `+inspectionJoins+` JOIN inspection_receipts e ON e.item_id=s.id WHERE `+filter+` AND r.id=? ORDER BY e.item_id,e.generation`, args...)
		if err != nil {
			return nil, err
		}
		for _, entry := range history {
			raw := entry["evidence"].(string)
			entry["evidence"] = json.RawMessage(raw)
			var ev inspectionEvidence
			if err := json.Unmarshal([]byte(raw), &ev); err != nil {
				return nil, err
			}
			entry["source"] = ev.Source
			category := "collector_summary"
			switch {
			case raw == "null" || ev.Gap != "":
				category = "gap_record"
			case ev.Bulletin != "":
				category = "offline_advisory_comparison"
			case ev.Source == "gateway-monitor-exporter":
				category = "collector_gateway_metric"
			}
			entry["evidence_category"] = category
		}
		meta, err := inspectionRows(tx, `SELECT r.id AS run_id,r.plan_id,p.name,p.owner,p.executor,p.timezone,p.weekday,p.clock,r.due,r.frozen,r.trigger_kind FROM inspection_runs r JOIN inspection_plans p ON p.id=r.plan_id WHERE r.id=?`, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"run": meta[0], "items": rows, "history": history, "checks": inspectionChecks, "generated_at": time.Now().UTC()}, nil
	case "", "overview":
		planArgs := append([]any{actor.active && actor.isAdmin}, args...)
		plans, err := inspectionRows(tx, `SELECT p.id AS plan_id,p.name,p.owner,p.executor,p.weekday,p.clock,p.timezone,p.next_due,p.active,(? AND p.active=1 AND `+inspectionOverdueSQL(gatewayDialect())+`) AS overdue,(SELECT disabled FROM users WHERE id=p.executor) AS executor_disabled,count(*) AS check_count FROM inspection_scope s JOIN inspection_plans p ON p.id=s.plan_id WHERE `+filter+` GROUP BY p.id,p.name,p.owner,p.executor,p.weekday,p.clock,p.timezone,p.next_due,p.active ORDER BY p.id DESC`, planArgs...)
		if err != nil {
			return nil, err
		}
		runs, err := inspectionRows(tx, `SELECT r.id AS run_id,p.name,p.owner,r.due,r.frozen,r.trigger_kind,count(*) AS planned,sum(s.status IN ('pass','finding')) AS checked,sum((`+inspectStateSQL()+`) IN ('insufficient','missing')) AS gaps `+inspectionJoins+` WHERE `+filter+` GROUP BY r.id,p.name,p.owner,r.due,r.frozen,r.trigger_kind ORDER BY r.due DESC,r.id DESC`, args...)
		if err != nil {
			return nil, err
		}
		catalog, err := inspectionRows(tx, `SELECT s.asset_id,s.cluster,s.instance,COALESCE(c.asset_id,'') AS cluster_id FROM `+inspectCatalog+` s LEFT JOIN cluster_inventory_sources c USING(cluster,instance) WHERE `+pred+` AND (?='' OR c.asset_id=?) ORDER BY s.asset_id,s.cluster,s.instance`, args...)
		return map[string]any{"plans": plans, "runs": runs, "assets": catalog, "checks": inspectionChecks}, err
	default:
		return nil, inspectErr(400, "unknown inspection view")
	}
}
