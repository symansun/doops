package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/user/doops/agent/api"
)

const missingExecutionManifest = "当前目标尚无已确认的资源清单，本次执行继续，请核对现场资源。"

type executionManifestAudit struct {
	revision uint64
	refs     []string
	warning  string
}

func usesExecutionManifest(tool string) bool {
	switch tool {
	case "doops_agent_prompt", "doops_shell", "doops_k8s", "doops_helm", "doops_bg", "doops_database_inventory":
		return true
	}
	return false
}

func (h *GatewayHub) pinExecutionManifest(ctx context.Context, a *GatewayAgent, auditID int64, p *api.ToolCallParams, skillSource string) (executionManifestAudit, error) {
	var use executionManifestAudit
	if !usesExecutionManifest(p.Name) {
		return use, nil
	}
	// The sync worker publishes only a manifest acknowledged on this connection.
	a.stateMu.RLock()
	execution := executionTargetManifest{Manifest: a.resourceSync.manifest, LastObservedAt: a.resourceSync.lastObservedAt}
	a.stateMu.RUnlock()
	if !a.AgentResourceSync || execution.Manifest.Revision == 0 {
		use.warning = missingExecutionManifest
		return use, nil
	}
	if session := extractSession(p.Arguments); session != "" {
		if err := validateSession(session); err != nil {
			return use, err
		}
	}
	raw, err := json.Marshal(execution)
	if err != nil {
		return use, err
	}
	refs, err := executionResourceRefs(execution.Manifest, p.Arguments, p.Name, skillSource)
	if err != nil {
		return use, err
	}
	ids, err := json.Marshal(refs)
	if err != nil {
		return use, err
	}
	res, err := h.store.db.ExecContext(ctx, `INSERT INTO agent_resource_audit(audit_id,manifest_revision,resource_refs_json) SELECT e.id,?,? FROM audit_events e JOIN agent_status a ON a.cluster=e.cluster AND a.instance=e.instance WHERE e.id=? AND a.cluster=? AND a.instance=? AND a.generation=? AND a.connected_at=? AND a.status='online'`, execution.Manifest.Revision, string(ids), auditID, a.Cluster, a.Instance, a.Generation, formatTime(a.ConnectedAt))
	if err != nil {
		return use, errors.New("target_manifest_audit_unavailable")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return use, errors.New("target_manifest_audit_unavailable")
	}
	if n != 1 {
		return use, errResourceSyncDisconnected
	}
	p.TargetManifest = raw
	use.revision, use.refs = execution.Manifest.Revision, refs
	return use, nil
}

func executionResourceRefs(m targetManifest, raw json.RawMessage, tool, skillSource string) ([]string, error) {
	var args struct {
		Instruction        string                       `json:"instruction"`
		CredentialRefs     []string                     `json:"credential_refs"`
		CredentialBindings []api.AgentCredentialBinding `json:"credential_bindings"`
		SQLCredential      string                       `json:"sql_credential"`
		Namespace          string                       `json:"namespace"`
		Asset              string                       `json:"asset_id"`
		Command            json.RawMessage              `json:"command"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	var text string
	if len(args.Command) > 0 && json.Unmarshal(args.Command, &text) != nil {
		var argv []string
		if err := json.Unmarshal(args.Command, &argv); err != nil {
			return nil, err
		}
		text = strings.Join(argv, " ")
	}
	words := strings.FieldsFunc(text+" "+args.Instruction+" "+skillSource, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._:%-", r))
	})
	explicit := make(map[string]bool, len(words))
	for _, word := range words {
		explicit[word] = true
	}
	credentialNames := args.CredentialRefs
	for _, binding := range args.CredentialBindings {
		credentialNames = append(credentialNames, binding.Name)
		if binding.DatabaseAssetID != "" {
			explicit["database/"+binding.DatabaseAssetID] = true
		}
	}
	if args.Namespace != "" {
		explicit[observedManifestRef("namespace", args.Namespace, m.Target.Cluster)] = true
	}
	if args.SQLCredential != "" {
		credentialNames = append(credentialNames, args.SQLCredential)
	}
	refs := []string{}
	for _, r := range m.Resources {
		credential := r.Kind == "credential" && slices.Contains(credentialNames, r.Name)
		asset := args.Asset != "" && r.Ref == r.Kind+"/"+args.Asset
		cluster := tool == "doops_k8s" && r.Kind == "cluster" && (r.State == "verified" || r.State == "discovered")
		if explicit[r.Ref] || credential || asset || cluster {
			refs = append(refs, r.Ref)
		}
	}
	return refs, nil
}

func loadManifestAudit(ctx context.Context, db *sql.DB, events []AuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	var installed int
	if err := db.QueryRowContext(ctx, gatewayExistingTableCountSQL(gatewayDialect(), 1), "agent_resource_audit").Scan(&installed); err != nil || installed != 1 {
		return err
	}
	args := make([]any, len(events))
	at := make(map[int64]int, len(events))
	for i, e := range events {
		args[i] = e.ID
		at[e.ID] = i
	}
	rows, err := db.QueryContext(ctx, `SELECT audit_id,manifest_revision,resource_refs_json FROM agent_resource_audit WHERE audit_id IN (`+gatewayInPlaceholders(len(args))+`)`, args...)
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
