package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type agentObservationRow struct {
	agentResourceObservation
	seen   string
	misses int
}

type declaredAgentResource struct {
	resource      targetManifestResource
	key, identity string
}

func observedManifestRef(kind, key, cluster string) string {
	if kind == "namespace" {
		return kind + "/" + cluster + "/" + key
	}
	return kind + "/" + key
}

func observationMissThreshold() (int, error) {
	value, ok := os.LookupEnv("DOOPS_AGENT_RESOURCE_MISS_THRESHOLD")
	if !ok {
		return 3, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 10000 {
		return 0, errors.New("invalid DOOPS_AGENT_RESOURCE_MISS_THRESHOLD")
	}
	return n, nil
}

func (s *GatewayStore) reconcileAgentResources(ctx context.Context, agent *GatewayAgent, snapshot *agentResourceSnapshot, digest string) (targetManifest, error) {
	if !s.agentResourceSyncReady(ctx) {
		return targetManifest{}, errResourceSyncMigration
	}
	status, err := s.targetResourceTransaction(ctx, agent.Cluster, agent.Instance, agent, snapshot, digest)
	return status.Manifest, err
}

func (s *GatewayStore) readTargetManifest(ctx context.Context, cluster, instance string) (targetManifestStatus, error) {
	if !s.agentResourceSyncReady(ctx) {
		return targetManifestStatus{}, errResourceSyncMigration
	}
	return s.targetResourceTransaction(ctx, cluster, instance, nil, nil, "")
}

func (s *GatewayStore) agentResourceSyncReady(ctx context.Context) bool {
	names := []string{
		"agent_resource_state", "agent_observations", "agent_resource_audit",
		"inventory_machines", "machine_diagnostic_bindings", "cluster_inventory_sources",
		"cluster_inventory_namespaces", "inventory_business_systems", "inventory_business_deployments",
		"inventory_resource_catalog", "inventory_agent_assets",
	}
	args := make([]any, len(names))
	for i, name := range names {
		args[i] = name
	}
	var count int
	if err := s.db.QueryRowContext(ctx, gatewayExistingTableCountSQL(gatewayDialectStore, len(names)), args...).Scan(&count); err != nil {
		return false
	}
	return count == len(names)
}

func (s *GatewayStore) targetResourceTransaction(ctx context.Context, cluster, instance string, agent *GatewayAgent, snapshot *agentResourceSnapshot, digest string) (targetManifestStatus, error) {
	var result targetManifestStatus
	threshold, err := observationMissThreshold()
	if err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var generation uint64
	var connected string
	err = tx.QueryRowContext(ctx, `SELECT status='online',generation,connected_at FROM agent_status WHERE cluster=? AND instance=? AND archived_at=''`, cluster, instance).Scan(&result.Online, &generation, &connected)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrAgentStatusNotFound
	}
	if err != nil {
		return result, err
	}
	if agent != nil && (!result.Online || generation != agent.Generation || connected != formatTime(agent.ConnectedAt.UTC())) {
		return result, errResourceSyncDisconnected
	}
	if _, err = tx.ExecContext(ctx, gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO agent_resource_state(cluster,instance) VALUES (?,?)", doNothing: true}), cluster, instance); err != nil {
		return result, err
	}
	now := formatTime(time.Now().UTC())
	if snapshot != nil {
		if agent == nil || !snapshot.valid() || agentResourceDigest(*snapshot) != digest {
			return result, errors.New("invalid_agent_observations")
		}
		if err = storeObservationEpoch(ctx, tx, cluster, instance, generation, *snapshot, now, threshold); err != nil {
			return result, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE agent_resource_state SET mode=?,kubernetes_available=?,supported=1,observation_hash=?,last_observed_at=?,connection_at=? WHERE cluster=? AND instance=?`, snapshot.Mode, snapshot.KubernetesAvailable, digest, now, connected, cluster, instance)
		if err != nil {
			return result, err
		}
	}
	m := &result.Manifest
	m.Target = targetManifestTarget{Cluster: cluster, Instance: instance}
	var available sql.NullBool
	var recorded, oldDigest string
	err = tx.QueryRowContext(ctx, `SELECT mode,kubernetes_available,supported,manifest_revision,manifest_hash,generated_at,last_observed_at,applied_revision,connection_at FROM agent_resource_state WHERE cluster=? AND instance=?`, cluster, instance).Scan(&m.Target.Mode, &available, &result.Supported, &m.Revision, &oldDigest, &m.GeneratedAt, &result.LastObservedAt, &result.AppliedRevision, &recorded)
	if err != nil {
		return result, err
	}
	if recorded != connected {
		result.Supported = false
		result.AppliedRevision = 0
		result.LastObservedAt = ""
		m.Target.Mode = ""
	} else if available.Valid {
		m.Target.KubernetesAvailable = &available.Bool
	}
	if !result.Supported && !result.Online {
		m.Target.Mode = ""
		m.Target.KubernetesAvailable = nil
	}
	declared, err := declaredAgentResources(ctx, tx, cluster, instance)
	if err != nil {
		return result, err
	}
	var observed []agentObservationRow
	if result.Supported {
		observed, err = readAgentObservations(ctx, tx, cluster, instance)
		if err != nil {
			return result, err
		}
	}
	m.Resources = mergeAgentResources(cluster, declared, observed, threshold)
	// Reserve the largest revision, digest and timestamp when calculating the transfer budget.
	m.Counts = make(map[string]int)
	for _, r := range m.Resources {
		m.Counts[r.Kind]++
	}
	if err = boundTargetManifest(m); err != nil {
		return result, err
	}
	m.Digest = m.contentDigest()
	if m.Digest != oldDigest {
		m.Revision++
		m.GeneratedAt = now
		_, err = tx.ExecContext(ctx, `UPDATE agent_resource_state SET manifest_revision=?,manifest_hash=?,generated_at=? WHERE cluster=? AND instance=?`, m.Revision, m.Digest, now, cluster, instance)
		if err != nil {
			return result, err
		}
	}
	return result, tx.Commit()
}

func storeObservationEpoch(ctx context.Context, tx *sql.Tx, cluster, instance string, generation uint64, snapshot agentResourceSnapshot, now string, threshold int) error {
	complete, err := json.Marshal(snapshot.Complete)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE agent_observations SET misses=MIN(misses+1, ?) WHERE cluster=? AND instance=? AND kind IN (SELECT value FROM json_each(?))`, threshold, cluster, instance, string(complete)); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO agent_observations(cluster,instance,kind,key,details,first_seen,last_seen,generation,misses) VALUES (?,?,?,?,?,?,?,?,0)", target: []string{"cluster", "instance", "kind", "key"}, set: []string{"details=excluded.details", "last_seen=excluded.last_seen", "generation=excluded.generation", "misses=0"}}))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, o := range snapshot.Resources {
		data, err := json.Marshal(o)
		if err != nil {
			return err
		}
		if _, err = stmt.ExecContext(ctx, cluster, instance, o.Kind, o.Key, string(data), now, now, generation); err != nil {
			return err
		}
	}
	return nil
}

func readAgentObservations(ctx context.Context, tx *sql.Tx, cluster, instance string) ([]agentObservationRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT details,last_seen,misses FROM agent_observations WHERE cluster=? AND instance=? ORDER BY kind,key`, cluster, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agentObservationRow
	for rows.Next() {
		var r agentObservationRow
		var raw []byte
		if err = rows.Scan(&raw, &r.seen, &r.misses); err != nil {
			return nil, err
		}
		if err = decodeResourceSync(raw, 4096, &r.agentResourceObservation); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func mergeAgentResources(cluster string, declared []declaredAgentResource, observed []agentObservationRow, threshold int) []targetManifestResource {
	byKey := make(map[string]agentObservationRow, len(observed))
	for _, o := range observed {
		byKey[o.Kind+"/"+o.Key] = o
	}
	used := map[string]bool{}
	out := make([]targetManifestResource, 0, len(declared)+len(observed))
	clusterRegistered := false
	for _, d := range declared {
		r := d.resource
		key := r.Kind + "/" + d.key
		o, ok := byKey[key]
		if r.Kind == "cluster" && d.key == "current" {
			clusterRegistered = true
		}
		r.State = "unverified"
		r.Source = []string{"declared"}
		if ok && d.key != "" {
			used[key] = true
			r.Source = append(r.Source, "observed")
			r.ObservedAt = o.seen
			r.Count = o.Count
			switch {
			case o.misses >= threshold:
				r.State = "lost"
				r.Reason = "连续完整观测中缺少该资源"
			case d.identity != "" && o.Identity != d.identity:
				r.State = "conflict"
				r.Reason = "现场身份与登记身份不一致"
			default:
				r.State = "verified"
			}
		}
		out = append(out, r)
	}
	for _, o := range observed {
		if used[o.Kind+"/"+o.Key] || o.misses > 0 {
			continue
		}
		r := targetManifestResource{Ref: observedManifestRef(o.Kind, o.Key, cluster), Kind: o.Kind, Name: o.Name, State: "discovered", Source: []string{"observed"}, ObservedAt: o.seen, Count: o.Count}
		r.CanRegister = (o.Kind == "cluster" && o.Identity != "") || (o.Kind == "namespace" && clusterRegistered && o.Identity != "")
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func boundTargetManifest(m *targetManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(data)+160 <= targetManifestMaxBytes {
		return nil
	}
	seen := make(map[string]int)
	m.Truncated = true
	m.Resources = slices.DeleteFunc(m.Resources, func(r targetManifestResource) bool { seen[r.Kind]++; return seen[r.Kind] > 50 })
	data, err = json.Marshal(m)
	if err != nil {
		return err
	}
	if len(data)+160 > targetManifestMaxBytes {
		return errors.New("manifest_metadata_too_large")
	}
	return nil
}

func (s *GatewayStore) saveAgentResourceApplied(ctx context.Context, agent *GatewayAgent, revision uint64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agent_resource_state SET applied_revision=? WHERE cluster=? AND instance=? AND manifest_revision=? AND EXISTS(SELECT 1 FROM agent_status a WHERE a.cluster=agent_resource_state.cluster AND a.instance=agent_resource_state.instance AND a.status='online' AND a.generation=? AND a.connected_at=?)`, revision, agent.Cluster, agent.Instance, revision, agent.Generation, formatTime(agent.ConnectedAt.UTC()))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errResourceSyncDisconnected
	}
	return nil
}

func declaredAgentResources(ctx context.Context, tx *sql.Tx, cluster, instance string) ([]declaredAgentResource, error) {
	// Project named columns only. Assignment alone does not identify the host or cluster observed by this Agent.
	query := `SELECT kind,id,name,key,identity,credential_type,` + gatewayUseColumn + ` FROM (
		 SELECT 'machine' AS kind, m.asset_id AS id, m.name AS name, 'host' AS key, COALESCE(b.host_sha256,'') AS identity, '' AS credential_type, '' AS ` + gatewayUseColumn + `
		 FROM inventory_machines m JOIN inventory_agent_assets aa ON aa.asset_id=m.asset_id AND aa.kind='machine' AND aa.cluster=?1 AND aa.instance=?2
		 LEFT JOIN machine_diagnostic_bindings b ON b.asset_id=m.asset_id AND b.cluster=?1 AND b.instance=?2
		 UNION ALL SELECT 'cluster', c.asset_id, c.name, 'current', c.kube_system_uid, '', '' FROM cluster_inventory_sources c WHERE c.cluster=?1 AND c.instance=?2
		 UNION ALL SELECT 'system', b.asset_id, b.name, b.asset_id, '', '', '' FROM inventory_business_systems b JOIN inventory_agent_assets aa ON aa.asset_id=b.asset_id AND aa.kind='system' AND aa.cluster=?1 AND aa.instance=?2
		 UNION ALL SELECT 'namespace', n.name, n.name, n.name, n.uid, '', '' FROM cluster_inventory_namespaces n WHERE n.cluster=?1 AND n.instance=?2
		 UNION ALL SELECT CASE c.kind WHEN 'website' THEN 'site' ELSE c.kind END, c.id, c.name, c.id, json_extract(c.details,'$.identity'), '', '' FROM inventory_resource_catalog c WHERE c.cluster=?1 AND c.instance=?2
		 UNION ALL SELECT 'credential', c.id, c.name, c.id, '', c.type, json_extract(g.scope_json,'$.use') FROM credentials c JOIN credential_grants g ON g.credential_id=c.id WHERE c.state='active' AND g.state='active' AND json_extract(g.scope_json,'$.cluster')=?1 AND json_extract(g.scope_json,'$.instance')=?2
	) ORDER BY 1,2`
	rows, err := tx.QueryContext(ctx, query, cluster, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []declaredAgentResource
	for rows.Next() {
		var d declaredAgentResource
		var id, use string
		r := &d.resource
		if err = rows.Scan(&r.Kind, &id, &r.Name, &d.key, &d.identity, &r.CredentialType, &use); err != nil {
			return nil, err
		}
		r.Ref = observedManifestRef(r.Kind, id, cluster)
		if !validManifestRef(r.Ref) || len(r.Name) > 256 || !inventoryMetadata(r.Name) || strings.Contains(r.Name, "://") {
			return nil, errors.New("invalid_registered_resource_metadata")
		}
		if r.Kind == "credential" {
			if !validCredentialUse(CredentialUse(use)) || credentialTypeAllowsUse(CredentialType(r.CredentialType), CredentialUse(use)) != nil {
				return nil, errors.New("invalid_registered_credential_metadata")
			}
			if len(out) > 0 && out[len(out)-1].resource.Ref == r.Ref {
				prev := &out[len(out)-1].resource
				if !slices.Contains(prev.Uses, use) {
					prev.Uses = append(prev.Uses, use)
				}
				continue
			}
			r.Uses = []string{use}
		}
		if r.Kind == "machine" || r.Kind == "cluster" || r.Kind == "system" {
			r.Source = []string{"declared"}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
