package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type machineInventoryInput struct {
	Tags        []string `json:"tags"`
	AssetID     string   `json:"asset_id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Address     string   `json:"address"`
	CPUCount    *int64   `json:"cpu_count"`
	MemoryGiB   *float64 `json:"memory_gib"`
	DiskGiB     *float64 `json:"disk_gib"`
	HostAssetID string   `json:"host_asset_id,omitempty"`
	Cluster     string   `json:"cluster"`
	Instance    string   `json:"instance"`
	CreatedAt   string   `json:"created_at,omitempty"`
}

type inventoryMachine struct {
	machineInventoryInput
	Revision      int64           `json:"revision"`
	TargetStatus  string          `json:"target_status"`
	LastSeen      string          `json:"last_seen,omitempty"`
	LastSampleMs  *int64          `json:"last_sample_ms,omitempty"`
	LoginAccounts json.RawMessage `json:"login_accounts,omitempty"`
}

var machineAssetID = regexp.MustCompile(`^[[:alnum:]][[:alnum:]_.-]{0,127}$`)
var machineSecretMarker = regexp.MustCompile(`(?i)\b(?:dgw_user_|dga_aen_|sk-[a-zA-Z0-9_-]{16,}|(?:password|token|api[_-]?key|secret|client-key-data|client-certificate-data)\s*[:=])`)

func inventoryMetadata(value string) bool {
	return !machineSecretMarker.MatchString(value) && !credentialScalarContainsInlineMaterial(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func safeMachineID(id string) bool { return machineAssetID.MatchString(id) && inventoryMetadata(id) }

func normalizeMachineCreatedAt(raw string) (string, *machineInventoryProblem) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	invalid := &machineInventoryProblem{http.StatusBadRequest, "invalid_created_at", "created_at must be a YYYY-MM-DD date or RFC3339 timestamp"}
	if len(s) > 64 || !inventoryMetadata(s) {
		return "", invalid
	}
	if len(s) == len("2006-01-02") {
		t, err := time.Parse("2006-01-02", s)
		if err != nil || t.Year() < 1970 || t.Year() > 9999 {
			return "", invalid
		}
		return t.UTC().Format(time.RFC3339), nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 1970 || t.Year() > 9999 {
		return "", invalid
	}
	return t.UTC().Format(time.RFC3339), nil
}

func (m machineInventoryInput) validate() *machineInventoryProblem {
	if _, issue := normalizeMachineCreatedAt(m.CreatedAt); issue != nil {
		return issue
	}
	switch {
	case !safeMachineID(m.AssetID):
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_asset_id", "asset_id must be a 1–128 character identifier using letters, digits, dots, underscores or hyphens"}
	case strings.TrimSpace(m.Name) == "" || len(m.Name) > 256 || !inventoryMetadata(m.Name):
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_name", "name must be a short display name without credential material"}
	case m.Kind != "physical" && m.Kind != "vm":
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_kind", "kind must be physical or vm"}
	case m.Address != "" && (len(m.Address) > 254 || !inventoryMetadata(m.Address) || (net.ParseIP(m.Address) == nil && !managedHelmHostnamePattern.MatchString(strings.TrimSuffix(m.Address, ".")))):
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_address", "address must be an IP address or hostname"}
	case m.CPUCount != nil && *m.CPUCount <= 0:
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_cpu_count", "cpu_count must be a positive integer"}
	case !validInventoryCapacity(m.MemoryGiB):
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_memory_gib", "memory_gib must be a finite positive number"}
	case !validInventoryCapacity(m.DiskGiB):
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_disk_gib", "disk_gib must be a finite positive number"}
	case m.HostAssetID != "" && (m.Kind != "vm" || m.HostAssetID == m.AssetID || !safeMachineID(m.HostAssetID)):
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_host_asset_id", "host_asset_id must reference another physical machine and is only allowed for a VM"}
	case (m.Cluster == "") != (m.Instance == ""):
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_target", "cluster and instance must be provided together"}
	case m.Cluster == "*" || m.Instance == "*" || !inventoryMetadata(m.Cluster) || !inventoryMetadata(m.Instance):
		return &machineInventoryProblem{http.StatusBadRequest, "invalid_target", "cluster and instance must identify a registered target"}
	}
	return nil
}

// Tags have no authorization meaning. Constrain their shape so they are safe
// metadata in a model-facing list, not connection strings or instructions.
// Supports flat labels ("qa") and single key=value pairs ("院系=计算机", "env=prod").
var machineTagPattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{M}\p{N}._-]*(?:=[\p{L}\p{N}][\p{L}\p{M}\p{N}._-]*)?$`)

func normalizeMachineTags(raw []string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	invalid := machineBadRequest("invalid_tags", "最多 20 个标签，每个限 32 字，支持标签或 key=value，使用文字、数字、点、横线或下划线，不含凭据")
	if len(raw) > 20 {
		return nil, invalid
	}
	tags := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, s := range raw {
		s = strings.ToLower(strings.TrimSpace(s))
		if utf8.RuneCountInString(s) > 32 || !machineTagPattern.MatchString(s) || !inventoryMetadata(s) {
			return nil, invalid
		}
		if !seen[s] {
			tags = append(tags, s)
			seen[s] = true
		}
	}
	sort.Strings(tags)
	return tags, nil
}

func validInventoryCapacity(n *float64) bool { return n == nil || (*n > 0 && !math.IsInf(*n, 0)) }

// A single read snapshot controls both membership and the host reference. An
// inventory record can outlive the gateway's offline agent-status retention.
func machineInventoryColumns(dialect gatewaySQLDialect) string {
	return `m.asset_id,m.name,m.kind,m.address,m.cpu_count,m.memory_gib,m.disk_gib,
 (SELECT ` + gatewayJSONOrderedArraySQL(dialect, "t.tag", "t.tag") + ` FROM inventory_machine_tags t WHERE t.asset_id=m.asset_id),
 COALESCE(host.asset_id,''),m.cluster,m.instance,m.created_at,m.revision,
 CASE WHEN m.cluster='' THEN 'unbound' ELSE COALESCE(a.status,'offline') END,COALESCE(a.last_seen,'')`
}

const machineStatusJoin = ` LEFT JOIN agent_status a ON a.cluster=m.cluster AND a.instance=m.instance `

func scanInventoryMachine(row interface{ Scan(...any) error }) (m inventoryMachine, err error) {
	var tagJSON string
	err = row.Scan(&m.AssetID, &m.Name, &m.Kind, &m.Address, &m.CPUCount, &m.MemoryGiB, &m.DiskGiB, &tagJSON, &m.HostAssetID, &m.Cluster, &m.Instance, &m.CreatedAt, &m.Revision, &m.TargetStatus, &m.LastSeen, (*[]byte)(&m.LoginAccounts))
	if err == nil {
		err = json.Unmarshal([]byte(tagJSON), &m.Tags)
	}
	if err == nil {
		// Order belongs to the reader: SQLite's json_group_array sorts inline,
		// MySQL's JSON_ARRAYAGG cannot, so both paths are normalized here.
		sort.Strings(m.Tags)
		m.LoginAccounts = normalizeLoginAccounts(m.LoginAccounts)
	}
	return
}

// normalizeLoginAccounts returns the account array sorted by username. MySQL
// aggregates without ORDER BY, so the array arrives in an arbitrary order; the
// JSON object field order is the struct's, which matches the json_object()
// spelling both dialects emit.
func normalizeLoginAccounts(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var accounts []struct {
		Username     string `json:"username"`
		CredentialID string `json:"credential_id"`
	}
	if err := json.Unmarshal(raw, &accounts); err != nil {
		return raw
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Username < accounts[j].Username })
	out, err := json.Marshal(accounts)
	if err != nil {
		return raw
	}
	return out
}

func listInventoryMachines(tx *sql.Tx, auth TokenAuth, tags ...string) ([]inventoryMachine, error) {
	actor, err := resolveInventoryActor(context.Background(), tx, auth)
	if err != nil {
		return nil, err
	}
	if !actor.active {
		return []inventoryMachine{}, nil
	}
	var args []any
	hostJoin := "LEFT JOIN inventory_machines host ON host.asset_id=m.host_asset_id"
	where := " WHERE 1=1"
	if !actor.isAdmin {
		hostJoin += " AND host.cluster<>'' AND host.instance<>'' AND EXISTS (SELECT 1 FROM grants g WHERE g.user_id=? AND g.cluster IN ('*',host.cluster) AND g.instance IN ('*',host.instance))"
		args = append(args, actor.userID)
		where += " AND m.cluster<>'' AND m.instance<>'' AND EXISTS (SELECT 1 FROM grants g WHERE g.user_id=? AND g.cluster IN ('*',m.cluster) AND g.instance IN ('*',m.instance))"
		args = append(args, actor.userID)
	}
	for _, tag := range tags {
		where += " AND EXISTS (SELECT 1 FROM inventory_machine_tags t WHERE t.asset_id=m.asset_id AND t.tag=?)"
		args = append(args, tag)
	}
	accountsExpr := "NULL"
	if actor.isAdmin {
		accountsExpr = machineLoginAccountsSQL(gatewayDialect())
	}
	rows, err := tx.Query(`SELECT `+machineInventoryColumns(gatewayDialect())+`, `+accountsExpr+` FROM inventory_machines m `+hostJoin+` `+machineStatusJoin+where+` ORDER BY m.asset_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []inventoryMachine{}
	for rows.Next() {
		m, err := scanInventoryMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const machineByIDFrom = ` FROM inventory_machines m LEFT JOIN inventory_machines host ON host.asset_id=m.host_asset_id ` + machineStatusJoin + ` WHERE m.asset_id=?`

func getInventoryMachine(tx *sql.Tx, id string) (inventoryMachine, error) {
	return scanInventoryMachine(tx.QueryRow(`SELECT `+machineInventoryColumns(gatewayDialect())+`, NULL`+machineByIDFrom, id))
}

func insertInventoryMachine(tx *sql.Tx, m machineInventoryInput) error {
	createdAt, issue := normalizeMachineCreatedAt(m.CreatedAt)
	if issue != nil {
		return *issue
	}
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := tx.Exec(`INSERT INTO inventory_machines(asset_id,name,kind,address,cpu_count,memory_gib,disk_gib,host_asset_id,cluster,instance,created_at)
 VALUES(?,?,?,?,?,?,?,NULLIF(?,''),?,?,?)`, m.AssetID, m.Name, m.Kind, m.Address, m.CPUCount, m.MemoryGiB, m.DiskGiB, m.HostAssetID, m.Cluster, m.Instance, createdAt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errInventoryInsertSkipped
	}
	return err
}

func saveInventoryMachine(tx *sql.Tx, m machineInventoryInput, revision *int64) (inventoryMachine, error) {
	var empty inventoryMachine
	var tagErr error
	if m.Tags, tagErr = normalizeMachineTags(m.Tags); tagErr != nil {
		return empty, tagErr
	}
	if normalized, issue := normalizeMachineCreatedAt(m.CreatedAt); issue != nil {
		return empty, *issue
	} else {
		m.CreatedAt = normalized
	}
	if issue := m.validate(); issue != nil {
		return empty, *issue
	}
	existing, err := getInventoryMachine(tx, m.AssetID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	switch {
	case revision == nil && err == nil:
		return empty, machineInventoryProblem{http.StatusConflict, "asset_id_exists", "asset_id already exists"}
	case revision != nil && err != nil:
		return empty, machineInventoryProblem{http.StatusNotFound, "machine_not_found", "machine not found"}
	case revision != nil && existing.Revision != *revision:
		return empty, machineInventoryProblem{http.StatusConflict, "revision_conflict", "revision changed; reload the machine"}
	}
	if m.HostAssetID != "" {
		host, err := getInventoryMachine(tx, m.HostAssetID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && host.Kind != "physical") {
			return empty, machineBadRequest("invalid_host_asset_id", inventoryHostRequired)
		}
		if err != nil {
			return empty, err
		}
	}
	if m.Cluster != "" {
		var found bool
		err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agent_status WHERE cluster=? AND instance=? AND archived_at='')`, m.Cluster, m.Instance).Scan(&found)
		if err != nil {
			return empty, err
		}
		if !found {
			return empty, machineBadRequest("target_not_registered", "cluster and instance must match a registered, unarchived target")
		}
	}
	if revision == nil {
		err = insertInventoryMachine(tx, m)
	} else {
		if existing.Kind == "physical" && m.Kind == "vm" {
			var hostsVM bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_machines WHERE host_asset_id=?)`, m.AssetID).Scan(&hostsVM); err != nil {
				return empty, err
			}
			if hostsVM {
				return empty, machineInventoryProblem{http.StatusConflict, "host_in_use", "kind cannot change while VMs reference this host"}
			}
		}
		createdAt := existing.CreatedAt
		if m.CreatedAt != "" {
			createdAt = m.CreatedAt
		}
		var res sql.Result
		res, err = tx.Exec(`UPDATE inventory_machines SET name=?,kind=?,address=?,cpu_count=?,memory_gib=?,disk_gib=?,host_asset_id=NULLIF(?,''),cluster=?,instance=?,created_at=?,revision=revision+1 WHERE asset_id=? AND revision=?`, m.Name, m.Kind, m.Address, m.CPUCount, m.MemoryGiB, m.DiskGiB, m.HostAssetID, m.Cluster, m.Instance, createdAt, m.AssetID, *revision)
		if err == nil {
			var changed int64
			changed, err = res.RowsAffected()
			if err == nil && changed != 1 {
				return empty, machineInventoryProblem{http.StatusConflict, "revision_conflict", "revision changed; reload the machine"}
			}
		}
	}
	if err != nil {
		return empty, err
	}
	// Omission by older clients preserves tags. An explicit [] clears them.
	if m.Tags != nil {
		if _, err := tx.Exec("DELETE FROM inventory_machine_tags WHERE asset_id=?", m.AssetID); err != nil {
			return empty, err
		}
		for _, tag := range m.Tags {
			if _, err := tx.Exec("INSERT INTO inventory_machine_tags(asset_id,tag) VALUES(?,?)", m.AssetID, tag); err != nil {
				return empty, err
			}
		}
	}
	// The mutation was authorized by requireInventoryAdmin in this transaction.
	return scanInventoryMachine(tx.QueryRow(`SELECT `+machineInventoryColumns(gatewayDialect())+`, `+machineLoginAccountsSQL(gatewayDialect())+machineByIDFrom, m.AssetID))
}
