package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/user/doops/agent/api"
)

func (h *GatewayHub) pinExecutionManifest(ctx context.Context, a *GatewayAgent, auditID int64, p *api.ToolCallParams) error {
	if !a.AgentResourceSync {
		return nil
	}
	switch p.Name {
	case "doops_agent_prompt", "doops_shell", "doops_k8s", "doops_helm", "doops_bg", "doops_database_inventory":
	default:
		return nil
	}
	session := extractSession(p.Arguments)
	if err := validateSession(session); err != nil {
		return err
	}
	// Recompute from this target's current grants before handing metadata to the Agent.
	// The manifest enriches audit references and prompt facts; it is not an
	// authorization gate, so a sync outage must not veto the execution itself.
	if err := h.syncAgentResources(ctx, a, false); err != nil {
		log.Printf("[gateway] execution manifest pin skipped for %s: %v", a.Key, err)
		return nil
	}
	a.stateMu.RLock()
	execution := executionTargetManifest{Manifest: a.resourceSync.manifest, LastObservedAt: a.resourceSync.lastObservedAt}
	a.stateMu.RUnlock()
	if execution.Manifest.Revision == 0 {
		return errTargetManifestUnavailable
	}
	raw, err := json.Marshal(execution)
	if err != nil {
		return err
	}
	refs := executionResourceRefs(execution.Manifest, p.Arguments, p.Name)
	ids, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	res, err := h.store.db.ExecContext(ctx, `INSERT INTO agent_resource_audit(audit_id,manifest_revision,resource_refs_json) SELECT e.id,?,? FROM audit_events e JOIN agent_status a ON a.cluster=e.cluster AND a.instance=e.instance WHERE e.id=? AND a.cluster=? AND a.instance=? AND a.generation=? AND a.connected_at=? AND a.status='online'`, execution.Manifest.Revision, string(ids), auditID, a.Cluster, a.Instance, a.Generation, formatTime(a.ConnectedAt))
	if err != nil {
		return errors.New("target_manifest_audit_unavailable")
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return errResourceSyncDisconnected
	}
	p.TargetManifest = raw
	return nil
}

func executionResourceRefs(m targetManifest, raw json.RawMessage, tool string) []string {
	var args struct {
		api.AgentPromptParams
		Namespace string   `json:"namespace"`
		Asset     string   `json:"asset_id"`
		Command   []string `json:"command"`
	}
	// Shell commands can be strings or argv; only explicit known references are retained.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	command := fields["command"]
	delete(fields, "command")
	fieldsRaw, _ := json.Marshal(fields)
	_ = json.Unmarshal(fieldsRaw, &args)
	var text string
	var argv []string
	if json.Unmarshal(command, &text) != nil {
		_ = json.Unmarshal(command, &argv)
		text = strings.Join(argv, " ")
	}
	words := strings.FieldsFunc(text+" "+args.Instruction, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._:-", r))
	})
	explicit := make(map[string]bool, len(words))
	for _, word := range words {
		explicit[word] = true
	}
	for _, name := range args.CredentialRefs {
		explicit["credential/"+name] = true
	}
	for _, binding := range args.CredentialBindings {
		explicit["credential/"+binding.Name] = true
	}
	if args.Namespace != "" {
		explicit[observedManifestRef("namespace", args.Namespace, m.Target.Cluster)] = true
	}
	if args.SQLCredential != "" {
		explicit["credential/"+args.SQLCredential] = true
	}
	refs := []string{}
	for _, r := range m.Resources {
		if explicit[r.Ref] || args.Asset != "" && r.Ref == r.Kind+"/"+args.Asset || tool == "doops_k8s" && r.Kind == "cluster" && (r.State == "verified" || r.State == "discovered") {
			refs = append(refs, r.Ref)
		}
	}
	return refs
}

func loadManifestAudit(ctx context.Context, db *sql.DB, events []AuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	var installed int
	if err := db.QueryRowContext(ctx, gatewayExistingTableCountSQL(gatewayDialectStore, 1), "agent_resource_audit").Scan(&installed); err != nil || installed != 1 {
		return err
	}
	ids := make([]int64, len(events))
	at := make(map[int64]int, len(events))
	for i, e := range events {
		ids[i] = e.ID
		at[e.ID] = i
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT audit_id,manifest_revision,resource_refs_json FROM agent_resource_audit WHERE audit_id IN (` + gatewayJSONEachValuesSQL(gatewayDialectStore, "?") + `)`, string(encoded))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var rev uint64
		var refs []byte
		if err := rows.Scan(&id, &rev, &refs); err != nil {
			return err
		}
		e := &events[at[id]]
		e.ManifestRevision = rev
		if err := json.Unmarshal(refs, &e.ResourceRefs); err != nil {
			return err
		}
	}
	return rows.Err()
}
