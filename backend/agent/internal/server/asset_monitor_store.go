package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const monitorSeriesKey = `cluster=? AND instance=? AND type=? AND asset_id=? AND namespace=? AND origin=? AND metric=? AND device_id=?`

func monitorKey(req monitorRequest, a monitorAsset, origin, metric, device string) []any {
	return []any{req.Cluster, req.Instance, a.Type, a.AssetID, a.Namespace, origin, metric, device}
}

type monitorAlertClose struct {
	OpenedMs  int64
	Threshold float64
	Duration  int64
}

// monitorAlertEvent builds the outbound event for one alert transition. It is
// called inside the sample transaction so the alert row and its webhook event
// commit together or not at all.
func monitorAlertEvent(req monitorRequest, p monitorInput, seriesID int64, eventType string, closed monitorAlertClose, ended int64, actor string) (PlatformEvent, error) {
	at := ended
	if at == 0 {
		at = closed.OpenedMs
	}
	state := "opened"
	if strings.HasSuffix(eventType, ".resolved") {
		state = "resolved"
	}
	data := map[string]any{
		"cluster":          req.Cluster,
		"instance":         req.Instance,
		"series_id":        seriesID,
		"asset_type":       p.Type,
		"asset_id":         p.AssetID,
		"namespace":        p.Namespace,
		"metric":           p.Metric,
		"device_id":        p.DeviceID,
		"state":            state,
		"opened_ms":        closed.OpenedMs,
		"ended_ms":         ended,
		"threshold":        closed.Threshold,
		"duration_seconds": closed.Duration,
	}
	return newPlatformEvent(eventType, time.UnixMilli(at), actor, req.Cluster, req.Instance, "", data,
		fmt.Sprintf("monitor:%d:%d:%s", seriesID, closed.OpenedMs, state))
}

func storeMonitorPoint(ctx context.Context, tx *sql.Tx, req monitorRequest, p monitorInput, origin, actor string) ([]PlatformEvent, error) {
	var events []PlatformEvent
	keys := monitorKey(req, p.monitorAsset, origin, p.Metric, p.DeviceID)
	if _, err := tx.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO asset_monitor_series(cluster,instance,type,asset_id,namespace,origin,metric,device_id) VALUES(?,?,?,?,?,?,?,?)", doNothing: true}), keys...); err != nil {
		return nil, err
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM asset_monitor_series WHERE `+monitorSeriesKey, keys...).Scan(&id); err != nil {
		return nil, err
	}
	var last sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(at_ms) FROM asset_monitor_samples WHERE series_id=?`, id).Scan(&last); err != nil {
		return nil, err
	}
	if last.Valid && p.At <= last.Int64 {
		var same bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM asset_monitor_samples WHERE series_id=? AND at_ms=? AND status=? AND `+gatewayNullSafeEqualsSQL(gatewayDialect(), "value", "?")+`)`, id, p.At, p.Status, p.Value).Scan(&same); err != nil {
			return nil, err
		}
		if same {
			return nil, nil
		}
		return nil, monitorErr(409, "monitor_sample_conflict")
	}
	if _, err := tx.Exec(`INSERT INTO asset_monitor_samples(series_id,at_ms,status,value) VALUES(?,?,?,?)`, id, p.At, p.Status, p.Value); err != nil {
		return nil, err
	}
	var threshold float64
	var duration, effective int64
	var since sql.NullInt64
	err := tx.QueryRow(`SELECT threshold,duration_seconds,effective_ms,first_above_ms FROM asset_monitor_rules WHERE series_id=?`, id).Scan(&threshold, &duration, &effective, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || p.At < effective {
		return nil, err
	}
	gap := last.Valid && p.At-last.Int64 > monitorGap.Milliseconds()
	if gap || p.Status != "ok" {
		ended := p.At
		if gap {
			ended = last.Int64 + monitorGap.Milliseconds()
		}
		if closed, ok, err := closeMonitorAlert(tx, id, ended, "interrupted"); err != nil {
			return nil, err
		} else if ok {
			ev, err := monitorAlertEvent(req, p, id, "monitor.alert.resolved", closed, ended, actor)
			if err != nil {
				return nil, err
			}
			events = append(events, ev)
		}
		since.Valid = false
	}
	if p.Status == "ok" && *p.Value > threshold {
		if !since.Valid {
			since = sql.NullInt64{Int64: p.At, Valid: true}
		}
		if p.At-since.Int64 >= duration*1000 {
			res, err := tx.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO asset_monitor_alerts(series_id,opened_ms,threshold,duration_seconds,state) VALUES(?,?,?,?,'open')", doNothing: true}), id, p.At, threshold, duration)
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				ev, err := monitorAlertEvent(req, p, id, "monitor.alert.opened", monitorAlertClose{OpenedMs: p.At, Threshold: threshold, Duration: duration}, p.At, actor)
				if err != nil {
					return nil, err
				}
				events = append(events, ev)
			}
		}
	} else {
		since.Valid = false
		if closed, ok, err := closeMonitorAlert(tx, id, p.At, "recovered"); err != nil {
			return nil, err
		} else if ok {
			ev, err := monitorAlertEvent(req, p, id, "monitor.alert.resolved", closed, p.At, actor)
			if err != nil {
				return nil, err
			}
			events = append(events, ev)
		}
	}
	if _, err = tx.Exec(`UPDATE asset_monitor_rules SET first_above_ms=? WHERE series_id=?`, since, id); err != nil {
		return nil, err
	}
	return events, nil
}

func closeMonitorAlert(tx *sql.Tx, id, at int64, state string) (monitorAlertClose, bool, error) {
	var closed monitorAlertClose
	err := tx.QueryRow(`SELECT opened_ms,threshold,duration_seconds FROM asset_monitor_alerts WHERE series_id=? AND state='open' ORDER BY opened_ms DESC LIMIT 1`, id).
		Scan(&closed.OpenedMs, &closed.Threshold, &closed.Duration)
	if errors.Is(err, sql.ErrNoRows) {
		return closed, false, nil
	}
	if err != nil {
		return closed, false, err
	}
	res, err := tx.Exec(`UPDATE asset_monitor_alerts SET ended_ms=?,state=? WHERE series_id=? AND state='open'`, at, state, id)
	if err != nil {
		return closed, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return closed, false, err
	}
	return closed, n > 0, nil
}

func closeRuleReconfiguredAlert(tx *sql.Tx, req monitorRequest, id, at int64, actor string) ([]PlatformEvent, error) {
	// Preserve the actual end of an incident if its source stopped before the rule edit/deletion.
	var latest sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(at_ms) FROM asset_monitor_samples WHERE series_id=?`, id).Scan(&latest); err != nil {
		return nil, err
	}
	ended, state := at, "reconfigured"
	if latest.Valid && at-latest.Int64 > monitorGap.Milliseconds() {
		ended, state = latest.Int64+monitorGap.Milliseconds(), "interrupted"
	}
	closed, ok, err := closeMonitorAlert(tx, id, ended, state)
	if err != nil || !ok {
		return nil, err
	}
	p := monitorInput{monitorAsset: req.monitorAsset, Metric: req.Metric, DeviceID: req.DeviceID}
	ev, err := monitorAlertEvent(req, p, id, "monitor.alert.resolved", closed, ended, actor)
	if err != nil {
		return nil, err
	}
	return []PlatformEvent{ev}, nil
}

func saveMonitorRule(tx *sql.Tx, req monitorRequest, at int64, actor string) ([]PlatformEvent, error) {
	var id int64
	if err := tx.QueryRow(`SELECT id FROM asset_monitor_series WHERE `+monitorSeriesKey, monitorKey(req, req.monitorAsset, req.Origin, req.Metric, req.DeviceID)...).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, monitorErr(404, "monitor_series_not_found")
		}
		return nil, err
	}
	result, err := tx.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO asset_monitor_rules(series_id,threshold,duration_seconds,effective_ms) VALUES(?,?,?,?)", target: []string{"series_id"}, set: []string{"threshold=excluded.threshold", "duration_seconds=excluded.duration_seconds", "effective_ms=excluded.effective_ms", "first_above_ms=NULL"}, where: "threshold<>excluded.threshold OR duration_seconds<>excluded.duration_seconds"}), id, req.Threshold, req.Duration, at)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return nil, err
	}
	return closeRuleReconfiguredAlert(tx, req, id, at, actor)
}

func deleteMonitorRule(tx *sql.Tx, req monitorRequest, at int64, actor string) ([]PlatformEvent, error) {
	var id int64
	if err := tx.QueryRow(`SELECT id FROM asset_monitor_series WHERE `+monitorSeriesKey, monitorKey(req, req.monitorAsset, req.Origin, req.Metric, req.DeviceID)...).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, monitorErr(404, "monitor_series_not_found")
		}
		return nil, err
	}
	result, err := tx.Exec(`DELETE FROM asset_monitor_rules WHERE series_id=?`, id)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return nil, err
	}
	return closeRuleReconfiguredAlert(tx, req, id, at, actor)
}
