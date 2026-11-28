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

func (s *GatewayStore) reconcileAgentResources(ctx context.Context, agent *GatewayAgent, snapshot *agentResourceSnapshot, digest string) (targetManifestStatus, error) {
	if !s.agentResourceSyncReady(ctx) {
		return targetManifestStatus{}, errResourceSyncMigration
	}
	return s.targetResourceTransaction(ctx, agent.Cluster, agent.Instance, agent, snapshot, digest)
}

func (s *GatewayStore) readTargetManifest(ctx context.Context, cluster, instance string) (targetManifestStatus, error) {
	if !s.agentResourceSyncReady(ctx) {
		return targetManifestStatus{}, errResourceSyncMigration
	}
	return s.targetResourceTransaction(ctx, cluster, instance, nil, nil, "")
}

func (s *GatewayStore) agentResourceSyncReady(ctx context.Context) bool {
	if s.resourceSyncReady.Load() {
		return true
	}
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
	if err := s.db.QueryRowContext(ctx, gatewayExistingTableCountSQL(gatewayDialect(), len(names)), args...).Scan(&count); err != nil {
		return false
	}
	if count == len(names) {
		s.resourceSyncReady.Store(true)
		return true
	}
	return false
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
	m := &result.Manifest
	m.Target = targetManifestTarget{Cluster: cluster, Instance: instance}
	var available sql.NullBool
	identityParam := gatewayIdentityTextFor(s.dialect, "?")
	statusQuery := `SELECT status='online',generation,connected_at,mode,kubernetes_available FROM agent_status WHERE cluster=` + identityParam + ` AND instance=` + identityParam + ` AND archived_at=''`
	if s.dialect == gatewayDialectMySQL {
		// Serialize projection and revision writes with reconnects and other
		// readers rebuilding this target on a different pooled connection.
		statusQuery += ` FOR UPDATE`
	}
	err = tx.QueryRowContext(ctx, statusQuery, cluster, instance).Scan(&result.Online, &generation, &connected, &m.Target.Mode, &available)
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
		if m.Target.Mode != snapshot.Mode || !available.Valid || available.Bool != snapshot.KubernetesAvailable {
			_, err = tx.ExecContext(ctx, `UPDATE agent_status SET mode=?,kubernetes_available=? WHERE cluster=? AND instance=? AND generation=? AND connected_at=?`, snapshot.Mode, snapshot.KubernetesAvailable, cluster, instance, generation, connected)
			if err != nil {
				return result, err
			}
			m.Target.Mode = snapshot.Mode
			available = sql.NullBool{Valid: true, Bool: snapshot.KubernetesAvailable}
		}
	}
	var recorded, oldDigest string
	err = tx.QueryRowContext(ctx, `SELECT supported,manifest_revision,manifest_hash,generated_at,last_observed_at,applied_revision,connection_at FROM agent_resource_state WHERE cluster=? AND instance=?`, cluster, instance).Scan(&result.Supported, &m.Revision, &oldDigest, &m.GeneratedAt, &result.LastObservedAt, &result.AppliedRevision, &recorded)
	if err != nil {
		return result, err
	}
	if recorded != connected {
		result.Supported = false
		result.AppliedRevision = 0
		result.LastObservedAt = ""
	}
	if available.Valid {
		m.Target.KubernetesAvailable = &available.Bool
	}
	declared, err := declaredAgentResources(ctx, tx, cluster, instance)
	if err != nil {
		return result, err
	}
	var observed []agentObservationRow
	// The first heartbeat on a replacement connection may still be pending.
	// Keep its last known projection stable; freshness and ACK are per connection.
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

// agentObservationMissesSQL renders the statement that ages every resource of a
// kind the Agent reported this round: misses grows by one, capped at threshold.
// Resources the Agent still reports are reset to zero by the upsert below, so
// what keeps a non-zero misses is a resource that disappeared inside a kind that
// was still reported; a kind that was not reported at all keeps its old value
// because it is not in the IN list.
//
// SQLite's MIN(a, b) is a scalar function, but MySQL has MIN only as an
// aggregate, so the two-argument call is Error 1064 (42000) there. Both LEAST
// and MIN return NULL when any argument is NULL, so the cap keeps its meaning on
// both engines; TestAgentObservationMissesAcrossEngines runs this exact
// statement against real SQLite and real MySQL.
func agentObservationMissesSQL(dialect gatewaySQLDialect) string {
	return "UPDATE agent_observations SET misses=" + gatewayScalarLeastSQL(dialect, "misses+1", "?") + " WHERE cluster=? AND instance=? AND kind IN (" + gatewayJSONEachValuesSQL(dialect, "?") + ")"
}

// agentObservationMissesInSQL renders the same update using explicit "?"
// placeholders instead of JSON_TABLE/json_each, so it executes identically on
// SQLite, MySQL 5.7, and MySQL 8.0/8.4.
func agentObservationMissesInSQL(dialect gatewaySQLDialect, count int) string {
	return "UPDATE agent_observations SET misses=" + gatewayScalarLeastSQL(dialect, "misses+1", "?") + " WHERE cluster=? AND instance=? AND kind IN (" + gatewayInPlaceholders(count) + ")"
}

// agentObservationUpsert is the observations upsert, rendered once per dialect by
// gatewayUpsertSQL. The key column goes through gatewayKeyColumn because KEY is
// reserved in MySQL 8.4; SQLite renders the same bare column as before, so the
// statement it sends is byte-identical to the pre-INT-44 text.
func agentObservationUpsert() gatewayUpsert {
	// The insert stays a string literal so the AST guards can enumerate this
	// upsert. Its column list spells the reserved column as a backtick-quoted
	// identifier: MySQL needs the quoting, SQLite accepts it as a quoted
	// identifier too, and gatewayMySQLUpsertInsert leaves an already-quoted
	// column alone instead of quoting it twice. The conflict target stays bare
	// because SQLite has no reserved word there and the unique-key parity guard
	// compares those names literally. Only the read and the predicates go
	// through gatewayKeyColumn, because they are not column lists.
	return gatewayUpsert{
		insert: "INSERT INTO agent_observations(cluster,instance,kind,`key`,details,first_seen,last_seen,generation,misses) VALUES (?,?,?,?,?,?,?,?,0)",
		target: []string{"cluster", "instance", "kind", "key"},
		set:    []string{"details=excluded.details", "last_seen=excluded.last_seen", "generation=excluded.generation", "misses=0"},
	}
}

// readAgentObservationsSQL renders the observation read for the live dialect.
func readAgentObservationsSQL() string { return readAgentObservationsSQLFor(gatewayDialect()) }

// readAgentObservationsSQLFor renders ORDER BY kind,<key column>. The column used
// to be spelled bare, which MySQL 8.4 rejects with Error 1064 (42000) near
// 'key' -- the error the target resource sync logged every 30 seconds. SQLite
// renders the same text it did before.
func readAgentObservationsSQLFor(dialect gatewaySQLDialect) string {
	return "SELECT details,last_seen,misses FROM agent_observations WHERE cluster=? AND instance=? ORDER BY kind," + gatewayKeyColumnFor(dialect)
}

const agentObservationUpsertBatchSize = 150

func storeObservationEpoch(ctx context.Context, tx *sql.Tx, cluster, instance string, generation uint64, snapshot agentResourceSnapshot, now string, threshold int) error {
	if len(snapshot.Complete) > 0 {
		args := make([]any, 0, 3+len(snapshot.Complete))
		args = append(args, threshold, cluster, instance)
		for _, kind := range snapshot.Complete {
			args = append(args, kind)
		}
		if _, err := tx.ExecContext(ctx, agentObservationMissesInSQL(gatewayDialect(), len(snapshot.Complete)), args...); err != nil {
			return err
		}
	}
	if len(snapshot.Resources) == 0 {
		return nil
	}
	upsert := agentObservationUpsert()
	for start := 0; start < len(snapshot.Resources); start += agentObservationUpsertBatchSize {
		end := start + agentObservationUpsertBatchSize
		if end > len(snapshot.Resources) {
			end = len(snapshot.Resources)
		}
		chunk := snapshot.Resources[start:end]
		args := make([]any, 0, len(chunk)*8)
		for _, o := range chunk {
			data, err := json.Marshal(o)
			if err != nil {
				return err
			}
			args = append(args, cluster, instance, o.Kind, o.Key, string(data), now, now, generation)
		}
		if _, err := tx.ExecContext(ctx, gatewayUpsertBatchSQL(upsert, len(chunk)), args...); err != nil {
			return err
		}
	}
	return nil
}

func readAgentObservations(ctx context.Context, tx *sql.Tx, cluster, instance string) ([]agentObservationRow, error) {
	rows, err := tx.QueryContext(ctx, readAgentObservationsSQL(), cluster, instance)
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
		if !r.valid() {
			return nil, errors.New("invalid_agent_observations")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func mergeAgentResources(cluster string, declared []declaredAgentResource, observed []agentObservationRow, threshold int) []targetManifestResource {
	byKey := make(map[string]agentObservationRow, len(observed))
	type identityEvidence struct {
		row  agentObservationRow
		live int
	}
	byIdentity := map[string]identityEvidence{}
	for _, o := range observed {
		byKey[o.Kind+"/"+o.Key] = o
		if o.Identity == "" || (o.Kind != "site" && o.Kind != "database") {
			continue
		}
		id := o.Kind + "/" + o.Identity
		e, exists := byIdentity[id]
		if !exists || o.misses < e.row.misses || o.misses == e.row.misses && o.seen > e.row.seen {
			e.row = o
		}
		if o.misses == 0 {
			e.live++
		}
		byIdentity[id] = e
	}
	registrations := map[string]int{}
	for _, d := range declared {
		if d.identity != "" {
			registrations[d.resource.Kind+"/"+d.identity]++
		}
	}
	used := map[string]bool{}
	out := make([]targetManifestResource, 0, len(declared)+len(observed))
	clusterVerified := false
	for _, d := range declared {
		r := d.resource
		r.Source = []string{"declared"}
		if r.State == "conflict" { // Several credentials share this target-local name.
			out = append(out, r)
			continue
		}
		r.State = "unverified"
		o, ok := byKey[r.Kind+"/"+d.key]
		ok = ok && d.key != "" && (r.Kind == "machine" || r.Kind == "cluster" || r.Kind == "namespace")
		if r.Kind == "site" || r.Kind == "database" {
			id := r.Kind + "/" + d.identity
			evidence, found := byIdentity[id]
			// A renamed discovery key may leave a missing row for the same endpoint.
			// Ambiguous live observations or catalog routes cannot identify one registration.
			ok = found && evidence.live <= 1 && registrations[id] == 1
			o = evidence.row
		}
		if ok && d.identity != "" {
			used[o.Kind+"/"+o.Key] = true
			r.Source = append(r.Source, "observed")
			r.ObservedAt, r.Count = o.seen, o.Count
			switch {
			case o.misses >= threshold:
				r.State, r.Reason = "lost", "连续完整观测中缺少该资源"
			case o.Identity == "":
				r.Reason = "观测缺少核验身份"
			case o.Identity != d.identity:
				r.State, r.Reason = "conflict", "现场身份与登记身份不一致"
			default:
				r.State = "verified"
				if r.Kind == "site" {
					r.Reason = "DoWeb 域名已匹配，协议和路径需单独核验"
				}
			}
			if r.Kind == "cluster" && d.key == "current" && r.State == "verified" && o.misses == 0 {
				clusterVerified = true
			}
		}
		out = append(out, r)
	}
	for _, o := range observed {
		if used[o.Kind+"/"+o.Key] || o.misses > 0 {
			continue
		}
		r := targetManifestResource{Ref: observedManifestRef(o.Kind, o.Key, cluster), Kind: o.Kind, Name: o.Name, State: "discovered", Source: []string{"observed"}, ObservedAt: o.seen, Count: o.Count}
		r.CanRegister = o.Identity != "" && (o.Kind == "cluster" || o.Kind == "namespace" && clusterVerified)
		switch o.Kind {
		case "site":
			r.Ref = "site/observed/" + o.Key
			r.Reason = manualCatalogRegistration(r.Kind)
		case "database":
			r.Ref = "database/observed/" + o.Key
			if strings.Contains(o.Key, "/") {
				r.Ref = "database/endpoint/" + strings.ReplaceAll(o.Key, "/", ":")
			}
			r.Reason = manualCatalogRegistration(r.Kind)
		}
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

// agentResourceAppliedPredicate is the WHERE clause of saveAgentResourceApplied.
// The UPDATE and its existence re-check share it verbatim so the two statements
// can never disagree about which rows count as an online, in-sync agent.
const agentResourceAppliedPredicate = `cluster=? AND instance=? AND manifest_revision=? AND EXISTS(SELECT 1 FROM agent_status a WHERE a.cluster=agent_resource_state.cluster AND a.instance=agent_resource_state.instance AND a.status='online' AND a.generation=? AND a.connected_at=?)`

// saveAgentResourceApplied records the manifest revision the agent confirmed.
//
// MySQL counts changed rows, not matched rows, for UPDATE: a heartbeat that
// already wrote the same applied_revision matches the row but changes nothing,
// so affected == 0 must not be read as "agent disconnected" (INT-61, which
// agent_resource_sync.go:72 then swallowed). A zero count is re-checked with an
// existence SELECT under the identical predicate in the same transaction; only a
// row that is really gone, offline, or on another generation/connection returns
// errResourceSyncDisconnected.
func (s *GatewayStore) saveAgentResourceApplied(ctx context.Context, agent *GatewayAgent, revision uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE agent_resource_state SET applied_revision=? WHERE `+agentResourceAppliedPredicate,
		revision, agent.Cluster, agent.Instance, revision, agent.Generation, formatTime(agent.ConnectedAt.UTC()))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		var present int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM agent_resource_state WHERE `+agentResourceAppliedPredicate,
			agent.Cluster, agent.Instance, revision, agent.Generation, formatTime(agent.ConnectedAt.UTC())).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			return errResourceSyncDisconnected
		}
	}
	return tx.Commit()
}

// declaredAgentResourcesSQL returns the statement declaredAgentResources runs.
// The seven unions repeat the target pair once per placeholder, in text order,
// so MySQL binds the same values SQLite used to bind by index; the INT-19 test
// executes this exact text against a real MySQL server.
func declaredAgentResourcesSQL() string {
	return declaredAgentResourcesSQLFor(gatewayDialect())
}

func declaredAgentResourcesSQLFor(dialect gatewaySQLDialect) string {
	useColumn := gatewayIdentifierFor(dialect, "use")
	keyColumn := gatewayIdentifierFor(dialect, "key")
	domain := gatewayJSONTextScalarFor(dialect, "json_extract(c.details,'$.domain')")
	database := gatewayJSONTextScalarFor(dialect, "json_array(json_extract(c.details,'$.engine'),json_extract(c.details,'$.host'),json_extract(c.details,'$.port'),json_extract(c.details,'$.database'))")
	grantUse := gatewayJSONTextScalarFor(dialect, "json_extract(g.scope_json,'$.use')")
	grantCluster := gatewayJSONTextScalarFor(dialect, "json_extract(g.scope_json,'$.cluster')")
	grantInstance := gatewayJSONTextScalarFor(dialect, "json_extract(g.scope_json,'$.instance')")
	return `SELECT kind,id,name,` + keyColumn + `,identity,credential_type,` + useColumn + ` FROM (
		SELECT 'machine' AS kind, m.asset_id AS id, m.name AS name, CASE WHEN b.host_sha256 IS NULL THEN '' ELSE 'host' END AS ` + keyColumn + `,
		 COALESCE(b.host_sha256,'') AS identity, '' AS credential_type, '' AS ` + useColumn + ` FROM inventory_machines m
		 LEFT JOIN machine_diagnostic_bindings b ON b.asset_id=m.asset_id AND b.cluster=m.cluster AND b.instance=m.instance AND b.cluster=? AND b.instance=?
		 WHERE (m.cluster=? AND m.instance=?) OR EXISTS (SELECT 1 FROM inventory_agent_assets a WHERE a.kind='machine' AND a.asset_id=m.asset_id AND a.cluster=? AND a.instance=?)
		UNION ALL SELECT 'cluster', c.asset_id, c.name, 'current', c.kube_system_uid, '', '' FROM cluster_inventory_sources c WHERE c.cluster=? AND c.instance=?
		UNION ALL SELECT 'cluster', a.asset_id, MIN(c.name), '', '', '', '' FROM inventory_agent_assets a JOIN cluster_inventory_sources c ON c.asset_id=a.asset_id
		 WHERE a.kind='cluster' AND a.cluster=? AND a.instance=? AND NOT EXISTS (SELECT 1 FROM cluster_inventory_sources n WHERE n.cluster=a.cluster AND n.instance=a.instance AND n.asset_id=a.asset_id) GROUP BY a.asset_id
		UNION ALL SELECT 'system', b.asset_id, b.name, '', '', '', '' FROM inventory_business_systems b WHERE
		 EXISTS (SELECT 1 FROM inventory_business_deployments d WHERE d.asset_id=b.asset_id AND d.cluster=? AND d.instance=?) OR
		 EXISTS (SELECT 1 FROM inventory_agent_assets a WHERE a.kind='system' AND a.asset_id=b.asset_id AND a.cluster=? AND a.instance=?)
		UNION ALL SELECT 'namespace', n.name, n.name, n.name, n.uid, '', '' FROM cluster_inventory_namespaces n WHERE n.cluster=? AND n.instance=?
		UNION ALL SELECT CASE c.kind WHEN 'website' THEN 'site' ELSE c.kind END, c.id, c.name, '',
		 CASE c.kind WHEN 'website' THEN COALESCE(` + domain + `,'') WHEN 'database' THEN ` + database + ` ELSE '' END, '', ''
		 FROM inventory_resource_catalog c WHERE c.cluster=? AND c.instance=? AND c.kind IN ('website','database','image')
		UNION ALL SELECT 'credential', c.name, c.name, c.id, '', c.type, ` + grantUse + ` FROM credentials c JOIN credential_grants g ON g.credential_id=c.id
		 WHERE c.state='active' AND g.state='active' AND ` + grantCluster + `=? AND ` + grantInstance + `=?
	) AS declared ORDER BY 1,2,4,7`
}

func declaredAgentResources(ctx context.Context, tx *sql.Tx, cluster, instance string) ([]declaredAgentResource, error) {
	rows, err := tx.QueryContext(ctx, declaredAgentResourcesSQL(),
		targetPairArgs(cluster, instance, 10)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []declaredAgentResource
	credentials := map[string]int{}
	for rows.Next() {
		var d declaredAgentResource
		var id, use string
		r := &d.resource
		if err = rows.Scan(&r.Kind, &id, &r.Name, &d.key, &d.identity, &r.CredentialType, &use); err != nil {
			return nil, err
		}
		if !validDeclaredResourceName(r.Kind, r.Name) {
			return nil, errors.New("invalid_registered_resource_metadata")
		}
		if r.Kind == "credential" {
			r.Ref = "credential/" + credentialManifestNameSegment(r.Name)
		} else {
			r.Ref = observedManifestRef(r.Kind, id, cluster)
			if !validObservationToken(id) || !validManifestRef(r.Ref) {
				return nil, errors.New("invalid_registered_resource_metadata")
			}
		}
		switch r.Kind {
		case "site":
			d.identity = siteObservationIdentity(d.identity)
		case "database":
			var parts [4]string
			if err := json.Unmarshal([]byte(d.identity), &parts); err != nil {
				return nil, errors.New("invalid_registered_database_metadata")
			}
			d.identity = databaseObservationIdentity(parts[0], parts[1], parts[2], parts[3])
		case "credential":
			if !validCredentialUse(CredentialUse(use)) || credentialTypeAllowsUse(CredentialType(r.CredentialType), CredentialUse(use)) != nil {
				return nil, errors.New("invalid_registered_credential_metadata")
			}
			if i, exists := credentials[r.Ref]; exists {
				prev := &out[i]
				if prev.key != d.key {
					prev.resource.State = "conflict"
					prev.resource.Reason = "目标绑定了多个同名凭据，请核对授权"
					prev.resource.CredentialType = ""
					prev.resource.Uses = nil
				} else if prev.resource.State != "conflict" && !slices.Contains(prev.resource.Uses, use) {
					prev.resource.Uses = append(prev.resource.Uses, use)
				}
				continue
			}
			credentials[r.Ref] = len(out)
			r.Uses = []string{use}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
