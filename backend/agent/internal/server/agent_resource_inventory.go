package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
)

func probeAgentMachine(ctx context.Context, r targetRuntime) ([]agentResourceObservation, bool) {
	// A container's machine-id is not evidence of the host binding.
	if ctx.Err() != nil || runtime.GOOS != "linux" || r.Container || r.InCluster {
		return nil, false
	}
	raw, err := os.ReadFile("/etc/machine-id")
	id := strings.TrimSpace(string(raw))
	if err != nil || !validLowerHex(id, 32) || id == strings.Repeat("0", 32) {
		return nil, false
	}
	fingerprint := sha256.Sum256([]byte(id))
	return []agentResourceObservation{{Kind: "machine", Key: "host", Name: "host", Identity: hex.EncodeToString(fingerprint[:])}}, true
}

// DoWeb publishes a domain but does not report a URL scheme or route.
func siteObservationIdentity(domain string) string {
	if !catalogHost(domain) || !inventoryMetadata(domain) {
		return ""
	}
	return agentResourceDigest(strings.ToLower(domain))
}

func databaseObservationIdentity(engine, host, port, name string) string {
	engine = strings.ToLower(engine)
	if engine == "postgresql" {
		engine = "postgres"
	}
	if engine == "" || !catalogHost(host) || !catalogPort(port) || !assetLabel.MatchString(name) || !inventoryMetadata(name) {
		return ""
	}
	// Only the Kubernetes service suffix denotes the same endpoint in both forms.
	host = strings.ToLower(host)
	if strings.HasSuffix(host, ".svc.cluster.local") {
		host = strings.TrimSuffix(host, ".cluster.local")
	}
	return agentResourceDigest([]string{engine, host, port, name})
}

func probeAgentDatabases(ctx context.Context) ([]agentResourceObservation, bool) {
	// The same inventory grouping owns controller/service identities for CLI and Agent sync.
	data, err := runAgentResourceProbe(ctx, 16<<20, "kubectl", "--request-timeout=4s", "get", "pods,services,deployments,statefulsets", "--all-namespaces", "--output", "json")
	if err != nil {
		return nil, false
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal(data, &list) != nil || list.Items == nil {
		return nil, false
	}
	candidates := databaseInventoryRows(list.Items, "", "")
	if len(candidates) > agentResourceProbeMaxRows {
		return nil, false
	}
	rows := make([]agentResourceObservation, 0, len(candidates))
	byKey := map[string]int{}
	for _, c := range candidates {
		if !inventoryNamespacePattern.MatchString(c.Namespace) || !validObservationToken(c.Service) || !catalogHost(c.Service) {
			return nil, false
		}
		key := strings.Join([]string{c.Namespace, c.Service, c.Engine}, "/")
		// A namespace and workload name can together exceed the 128-byte observation key.
		if len(key) > 128 {
			key = agentResourceDigest(key)
		}
		identity := databaseObservationIdentity(c.Engine, c.Host, strconv.Itoa(c.Port), c.DatabaseName)
		if i, exists := byKey[key]; exists {
			// Unrelated workloads and services may have the same Kubernetes name.
			if rows[i].Identity != identity {
				rows[i].Identity = ""
			}
			continue
		}
		byKey[key] = len(rows)
		rows = append(rows, agentResourceObservation{Kind: "database", Key: key, Name: c.Service, Identity: identity})
	}
	return rows, true
}

func manualCatalogRegistration(kind string) string {
	if kind == "site" {
		return "请在目标的网站目录核对名称、协议、域名和路径后登记"
	}
	return "请在目标的数据库目录核对名称、引擎、主机、端口和库名后登记"
}

func (s *GatewayStore) registerObservedResource(ctx context.Context, auth TokenAuth, cluster, instance, ref string) error {
	return s.catalogChange(ctx, auth, "target_manifest:register", func(tx *sql.Tx) error {
		// Importing Gateway cluster inventory is already an administrator operation.
		if err := authorizeInventory(ctx, tx, auth, inventoryRequest{Cluster: cluster, Instance: instance, Action: "import"}); err != nil {
			var p inventoryFailure
			if errors.As(err, &p) {
				return assetIssue(p.status, p.code)
			}
			return err
		}
		kind, key, _ := strings.Cut(ref, "/")
		if kind == "namespace" {
			key = strings.TrimPrefix(key, cluster+"/")
		}
		switch kind {
		case "site", "database":
			return machineInventoryProblem{409, "resource_requires_inventory_fields", manualCatalogRegistration(kind)}
		}
		if (kind != "cluster" && kind != "namespace") || !validObservationToken(key) || observedManifestRef(kind, key, cluster) != ref {
			return assetIssue(409, "resource_requires_inventory_fields")
		}
		var data []byte
		var seen string
		err := tx.QueryRowContext(ctx, `SELECT o.details,o.last_seen FROM agent_observations o JOIN agent_status a USING(cluster,instance) JOIN agent_resource_state s USING(cluster,instance) WHERE o.cluster=? AND o.instance=? AND kind=? AND `+gatewayKeyColumn()+`=? AND misses=0 AND o.generation=a.generation AND o.last_seen=s.last_observed_at AND a.connected_at=s.connection_at AND a.status='online' AND s.supported=1`, cluster, instance, kind, key).Scan(&data, &seen)
		if errors.Is(err, sql.ErrNoRows) {
			return assetIssue(409, "resource_observation_stale")
		}
		if err != nil {
			return err
		}
		var o agentResourceObservation
		if decodeResourceSync(data, 4096, &o) != nil || o.Identity == "" {
			return assetIssue(409, "resource_identity_unavailable")
		}
		var id, name, uid string
		err = tx.QueryRowContext(ctx, `SELECT asset_id,name,kube_system_uid FROM cluster_inventory_sources WHERE cluster=? AND instance=?`, cluster, instance).Scan(&id, &name, &uid)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if kind == "cluster" {
			if err == nil {
				if uid == o.Identity {
					return nil
				}
				return assetIssue(409, "cluster_identity_conflict")
			}
			// A UID identifies the cluster even when two independently named Agents see it.
			id = "k8s-" + o.Identity
			name = cluster
			err = tx.QueryRowContext(ctx, `SELECT asset_id,name FROM cluster_inventory_sources WHERE kube_system_uid=? ORDER BY asset_id LIMIT 1`, o.Identity).Scan(&id, &name)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			var conflicts bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cluster_inventory_sources WHERE asset_id=? AND kube_system_uid<>?)`, id, o.Identity).Scan(&conflicts); err != nil {
				return err
			}
			if conflicts {
				return assetIssue(409, "cluster_identity_conflict")
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO cluster_inventory_sources(cluster,instance,asset_id,name,kube_system_uid,verified_at,verified_by,scope) VALUES(?,?,?,?,?,?,?,'list_namespaces')`, cluster, instance, id, name, o.Identity, seen, auth.UserID)
			return err
		}
		if err != nil {
			return assetIssue(409, "cluster_registration_required")
		}
		var currentUID string
		err = tx.QueryRowContext(ctx, `SELECT `+gatewayJSONTextScalarFor(gatewayDialect(), "json_extract(details,'$.identity')")+` FROM agent_observations WHERE cluster=? AND instance=? AND kind='cluster' AND `+gatewayKeyColumn()+`='current' AND misses=0 AND last_seen=?`, cluster, instance, seen).Scan(&currentUID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && uid != currentUID {
			return assetIssue(409, "cluster_identity_conflict")
		}
		if err != nil {
			return err
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT uid FROM cluster_inventory_namespaces WHERE cluster=? AND instance=? AND name=?`, cluster, instance, key).Scan(&existing)
		if err == nil {
			if existing == o.Identity {
				return nil
			}
			return assetIssue(409, "namespace_identity_conflict")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cluster_inventory_namespaces(cluster,instance,name,uid,verified_at,verified_by,scope,namespace_observed_at,pods_listed_at) VALUES(?,?,?,?,?,?,'list_namespaces',?,'')`, cluster, instance, key, o.Identity, seen, auth.UserID, seen)
		return err
	})
}
