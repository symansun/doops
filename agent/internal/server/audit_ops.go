package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 审计导出
// ---------------------------------------------------------------------------

var auditCSVHeader = []string{
	"id", "started_at", "ended_at", "user_id", "token_id", "cluster", "instance",
	"action", "session", "status", "error", "command_summary", "bytes_in", "bytes_out", "tail",
}

func auditRecordFromEvent(event AuditEvent) auditRecord {
	record := auditRecord{
		ID:             event.ID,
		UserID:         event.UserID,
		TokenID:        event.TokenID,
		Cluster:        event.Cluster,
		Instance:       event.Instance,
		Action:         string(event.Action),
		Session:        event.Session,
		CommandSummary: event.CommandSummary,
		Status:         event.Status,
		Error:          event.Error,
		Tail:           event.Tail,
		BytesIn:        event.BytesIn,
		BytesOut:       event.BytesOut,
		StartedAt:      event.StartedAt.UTC().Format(time.RFC3339),
	}
	if !event.EndedAt.IsZero() {
		record.EndedAt = event.EndedAt.UTC().Format(time.RFC3339)
	}
	return record
}

// writeAuditCSV 以 CSV（UTF-8，逗号分隔）导出审计记录，供客户存档或导入 BI。
func writeAuditCSV(w http.ResponseWriter, events []AuditEvent) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="doops-audit.csv"`)
	cw := csv.NewWriter(w)
	if err := cw.Write(auditCSVHeader); err != nil {
		return err
	}
	for _, event := range events {
		record := auditRecordFromEvent(event)
		row := []string{
			strconv.FormatInt(record.ID, 10), record.StartedAt, record.EndedAt, record.UserID, record.TokenID,
			record.Cluster, record.Instance, record.Action, record.Session, record.Status,
			record.Error, record.CommandSummary,
			strconv.FormatInt(record.BytesIn, 10), strconv.FormatInt(record.BytesOut, 10), record.Tail,
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// writeAuditJSONL 以 JSONL 导出（每行一条记录），便于 jq / 日志管道处理。
func writeAuditJSONL(w http.ResponseWriter, events []AuditEvent) error {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="doops-audit.jsonl"`)
	encoder := json.NewEncoder(w)
	for _, event := range events {
		if err := encoder.Encode(auditRecordFromEvent(event)); err != nil {
			return err
		}
	}
	return nil
}

// parseAuditTimeParam 解析审计查询的 RFC3339 时间参数；空串返回零值。
func parseAuditTimeParam(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

// ---------------------------------------------------------------------------
// 留存策略
// ---------------------------------------------------------------------------

// pruneAuditOnce 按留存天数清理历史审计；days<=0 表示不清理。
func (h *GatewayHub) pruneAuditOnce(days int, now time.Time) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	return h.store.DeleteAuditBefore(now.UTC().AddDate(0, 0, -days))
}

// StartAuditRetention 启动后台留存清理：启动即执行一次，之后每 24h 一次。
// 仅在 opts.AuditRetentionDays > 0 时生效；清理前应先做一次导出（见 admin audit export）。
func (h *GatewayHub) StartAuditRetention() {
	days := h.opts.AuditRetentionDays
	if days <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			deleted, err := h.pruneAuditOnce(days, time.Now())
			if err != nil {
				log.Printf("audit retention prune failed: %v", err)
			} else if deleted > 0 {
				log.Printf("audit retention: pruned %d audit events older than %d days", deleted, days)
			}
			<-ticker.C
		}
	}()
}

// ---------------------------------------------------------------------------
// 高危操作告警
// ---------------------------------------------------------------------------

// auditAlertText 生成告警文本；detail 截断避免超长消息被 webhook 拒收。
func auditAlertText(kind, userID, cluster, instance, action, detail string) string {
	detail = strings.TrimSpace(detail)
	if len([]rune(detail)) > 200 {
		detail = string([]rune(detail)[:200]) + "..."
	}
	text := fmt.Sprintf("[DoOps] %s user=%s target=%s/%s action=%s", kind, userID, cluster, instance, action)
	if detail != "" {
		text += " detail=" + detail
	}
	return text
}

// postAlert 同步发送告警（便于单测）；飞书/企业微信自定义机器人可直接消费
// {"msg_type":"text","content":{"text":"..."}} 文本消息格式。
func postAlert(ctx context.Context, webhook, text string) error {
	body, err := json.Marshal(map[string]interface{}{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("alert webhook returned HTTP %s", resp.Status)
}

// notifyAlert 异步推送高危操作告警；未配置 AlertWebhook 时静默跳过。
// 告警失败只记日志，不影响业务路径。
func (h *GatewayHub) notifyAlert(kind, userID, cluster, instance, action, detail string) {
	webhook := strings.TrimSpace(h.opts.AlertWebhook)
	if webhook == "" {
		return
	}
	text := auditAlertText(kind, userID, cluster, instance, action, detail)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := postAlert(ctx, webhook, text); err != nil {
			log.Printf("alert webhook failed (%s): %v", kind, err)
		}
	}()
}
