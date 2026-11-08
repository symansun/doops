package server

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrEdgeRouteExists   = errors.New("edge route hostname already exists")
	ErrEdgeRouteNotFound = errors.New("edge route not found")
)

const edgeRouteColumns = `id, hostname, cluster, instance, backend, mode, enabled, max_conns, note, created_at, updated_at`

func (s *GatewayStore) migrateEdge() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS edge_routes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hostname TEXT NOT NULL,
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			backend TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'tls-passthrough',
			enabled INTEGER NOT NULL DEFAULT 1,
			max_conns INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_edge_routes_hostname ON edge_routes(hostname)`,
		`CREATE TABLE IF NOT EXISTS edge_route_revision (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			version INTEGER NOT NULL
		)`,
		`INSERT OR IGNORE INTO edge_route_revision (id, version) VALUES (1, 1)`,
		`CREATE TABLE IF NOT EXISTS edge_enrollments (
			id TEXT PRIMARY KEY,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			prefix TEXT NOT NULL,
			hash TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS edge_agent_status (
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'offline',
			sessions INTEGER NOT NULL DEFAULT 0,
			remote TEXT NOT NULL DEFAULT '',
			generation INTEGER NOT NULL DEFAULT 0,
			connected_at TEXT NOT NULL DEFAULT '',
			last_seen TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(cluster, instance)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// CreateEdgeRouteRequest 新增一条域名映射。Disabled 且不带 Backend 时是一条 deny 规则。
type CreateEdgeRouteRequest struct {
	Hostname string `json:"hostname"`
	Cluster  string `json:"cluster"`
	Instance string `json:"instance"`
	Backend  string `json:"backend"`
	Mode     string `json:"mode"`
	Disabled bool   `json:"disabled"`
	MaxConns int    `json:"max_conns"`
	Note     string `json:"note"`
}

// UpdateEdgeRouteRequest 局部更新。hostname 是路由身份，不可改，
// 改域名请删除后重建，避免唯一索引上的隐式搬迁。
type UpdateEdgeRouteRequest struct {
	Cluster  *string `json:"cluster,omitempty"`
	Instance *string `json:"instance,omitempty"`
	Backend  *string `json:"backend,omitempty"`
	Mode     *string `json:"mode,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
	MaxConns *int    `json:"max_conns,omitempty"`
	Note     *string `json:"note,omitempty"`
}

type edgeRouteInput struct {
	Hostname string
	Cluster  string
	Instance string
	Backend  string
	Mode     string
	Enabled  bool
	MaxConns int
	Note     string
}

func validateEdgeRouteInput(in edgeRouteInput) (edgeRouteInput, error) {
	hostname, err := parseEdgeHostname(in.Hostname)
	if err != nil {
		return edgeRouteInput{}, err
	}
	mode, err := normalizeEdgeRouteMode(in.Mode)
	if err != nil {
		return edgeRouteInput{}, err
	}
	out := edgeRouteInput{
		Hostname: hostname.Canonical,
		Cluster:  strings.TrimSpace(in.Cluster),
		Instance: strings.TrimSpace(in.Instance),
		Mode:     mode,
		Enabled:  in.Enabled,
		MaxConns: in.MaxConns,
		Note:     strings.TrimSpace(in.Note),
	}
	if out.MaxConns < 0 {
		return edgeRouteInput{}, fmt.Errorf("max_conns %d must not be negative", in.MaxConns)
	}
	if strings.TrimSpace(in.Backend) == "" {
		// deny 规则只需要 hostname；启用的路由必须有转发目标，否则命中后无处可去。
		if out.Enabled {
			return edgeRouteInput{}, errors.New("an enabled edge route requires a backend")
		}
		if out.Cluster != "" || out.Instance != "" {
			return edgeRouteInput{}, errors.New("a deny route must not name a cluster or instance")
		}
		return out, nil
	}
	backend, err := normalizeEdgeBackend(in.Backend)
	if err != nil {
		return edgeRouteInput{}, err
	}
	out.Backend = backend
	if out.Cluster == "" || out.Instance == "" {
		return edgeRouteInput{}, errors.New("a forwarding edge route requires both cluster and instance")
	}
	return out, nil
}

func (s *GatewayStore) CreateEdgeRoute(req CreateEdgeRouteRequest) (EdgeRoute, error) {
	input, err := validateEdgeRouteInput(edgeRouteInput{
		Hostname: req.Hostname,
		Cluster:  req.Cluster,
		Instance: req.Instance,
		Backend:  req.Backend,
		Mode:     req.Mode,
		Enabled:  !req.Disabled,
		MaxConns: req.MaxConns,
		Note:     req.Note,
	})
	if err != nil {
		return EdgeRoute{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return EdgeRoute{}, err
	}
	defer tx.Rollback()

	var conflict int64
	switch err := tx.QueryRow(`SELECT id FROM edge_routes WHERE hostname = ?`, input.Hostname).Scan(&conflict); {
	case err == nil:
		return EdgeRoute{}, fmt.Errorf("%w: %s is route %d", ErrEdgeRouteExists, input.Hostname, conflict)
	case errors.Is(err, sql.ErrNoRows):
	default:
		return EdgeRoute{}, err
	}

	now := formatTime(time.Now().UTC())
	result, err := tx.Exec(`INSERT INTO edge_routes
		(hostname, cluster, instance, backend, mode, enabled, max_conns, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.Hostname, input.Cluster, input.Instance, input.Backend, input.Mode,
		boolInt(input.Enabled), input.MaxConns, input.Note, now, now)
	if err != nil {
		return EdgeRoute{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return EdgeRoute{}, err
	}
	if err := bumpEdgeRouteRevision(tx); err != nil {
		return EdgeRoute{}, err
	}
	route, err := readEdgeRoute(tx, id)
	if err != nil {
		return EdgeRoute{}, err
	}
	if err := tx.Commit(); err != nil {
		return EdgeRoute{}, err
	}
	return route, nil
}

func (s *GatewayStore) UpdateEdgeRoute(id int64, req UpdateEdgeRouteRequest) (EdgeRoute, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return EdgeRoute{}, err
	}
	defer tx.Rollback()

	current, err := readEdgeRoute(tx, id)
	if err != nil {
		return EdgeRoute{}, err
	}
	merged := edgeRouteInput{
		Hostname: current.Hostname,
		Cluster:  current.Cluster,
		Instance: current.Instance,
		Backend:  current.Backend,
		Mode:     current.Mode,
		Enabled:  current.Enabled,
		MaxConns: current.MaxConns,
		Note:     current.Note,
	}
	if req.Cluster != nil {
		merged.Cluster = *req.Cluster
	}
	if req.Instance != nil {
		merged.Instance = *req.Instance
	}
	if req.Backend != nil {
		merged.Backend = *req.Backend
	}
	if req.Mode != nil {
		merged.Mode = *req.Mode
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
	}
	if req.MaxConns != nil {
		merged.MaxConns = *req.MaxConns
	}
	if req.Note != nil {
		merged.Note = *req.Note
	}
	input, err := validateEdgeRouteInput(merged)
	if err != nil {
		return EdgeRoute{}, err
	}
	now := formatTime(time.Now().UTC())
	if _, err := tx.Exec(`UPDATE edge_routes
		SET cluster = ?, instance = ?, backend = ?, mode = ?, enabled = ?, max_conns = ?, note = ?, updated_at = ?
		WHERE id = ?`,
		input.Cluster, input.Instance, input.Backend, input.Mode,
		boolInt(input.Enabled), input.MaxConns, input.Note, now, id); err != nil {
		return EdgeRoute{}, err
	}
	if err := bumpEdgeRouteRevision(tx); err != nil {
		return EdgeRoute{}, err
	}
	route, err := readEdgeRoute(tx, id)
	if err != nil {
		return EdgeRoute{}, err
	}
	if err := tx.Commit(); err != nil {
		return EdgeRoute{}, err
	}
	return route, nil
}

func (s *GatewayStore) DeleteEdgeRoute(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`DELETE FROM edge_routes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: id %d", ErrEdgeRouteNotFound, id)
	}
	if err := bumpEdgeRouteRevision(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GatewayStore) ListEdgeRoutes() ([]EdgeRoute, error) {
	rows, err := s.db.Query(`SELECT ` + edgeRouteColumns + ` FROM edge_routes ORDER BY hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []EdgeRoute
	for rows.Next() {
		route, err := scanEdgeRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return routes, nil
}

// LoadEdgeRouteSnapshot 在一个事务里同时读出修订号与全部路由，供 EdgeRegistry 原子替换。
func (s *GatewayStore) LoadEdgeRouteSnapshot() (*edgeRouteSnapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var version int64
	if err := tx.QueryRow(`SELECT version FROM edge_route_revision WHERE id = 1`).Scan(&version); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT ` + edgeRouteColumns + ` FROM edge_routes ORDER BY hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []EdgeRoute
	for rows.Next() {
		route, err := scanEdgeRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return newEdgeRouteSnapshot(version, routes)
}

func bumpEdgeRouteRevision(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE edge_route_revision SET version = version + 1 WHERE id = 1`)
	return err
}

func readEdgeRoute(tx *sql.Tx, id int64) (EdgeRoute, error) {
	route, err := scanEdgeRoute(tx.QueryRow(`SELECT `+edgeRouteColumns+` FROM edge_routes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return EdgeRoute{}, fmt.Errorf("%w: id %d", ErrEdgeRouteNotFound, id)
	}
	return route, err
}

type edgeRowScanner interface {
	Scan(dest ...any) error
}

func scanEdgeRoute(scanner edgeRowScanner) (EdgeRoute, error) {
	var (
		route   EdgeRoute
		enabled int
		created string
		updated string
	)
	if err := scanner.Scan(&route.ID, &route.Hostname, &route.Cluster, &route.Instance,
		&route.Backend, &route.Mode, &enabled, &route.MaxConns, &route.Note, &created, &updated); err != nil {
		return EdgeRoute{}, err
	}
	route.Enabled = enabled != 0
	route.CreatedAt, _ = parseTime(created)
	route.UpdatedAt, _ = parseTime(updated)
	return route, nil
}

type EdgeEnrollment struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Instance  string    `json:"instance"`
	Prefix    string    `json:"prefix"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
}

type CreatedEdgeEnrollment struct {
	ID        string `json:"id"`
	Prefix    string `json:"prefix"`
	Plaintext string `json:"-"`
	Cluster   string `json:"cluster"`
	Instance  string `json:"instance"`
}

func (s *GatewayStore) CreateEdgeEnrollment(cluster, instance string) (CreatedEdgeEnrollment, error) {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if cluster == "" || instance == "" {
		return CreatedEdgeEnrollment{}, errors.New("edge enrollment requires cluster and instance")
	}
	id := "een_" + randomHex(12)
	prefix := "dge_een"
	plain := prefix + "_" + id + "_" + randomHex(32)
	hash, err := bcrypt.GenerateFromPassword(tokenPassword(plain), bcrypt.DefaultCost)
	if err != nil {
		return CreatedEdgeEnrollment{}, err
	}
	now := formatTime(time.Now().UTC())
	if _, err := s.db.Exec(`INSERT INTO edge_enrollments (id, cluster, instance, prefix, hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, id, cluster, instance, prefix, string(hash), now); err != nil {
		return CreatedEdgeEnrollment{}, err
	}
	return CreatedEdgeEnrollment{ID: id, Prefix: prefix, Plaintext: plain, Cluster: cluster, Instance: instance}, nil
}

func (s *GatewayStore) VerifyEdgeEnrollment(token, cluster, instance string) (EdgeEnrollment, error) {
	token = strings.TrimSpace(token)
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if token == "" || cluster == "" || instance == "" {
		return EdgeEnrollment{}, ErrUnauthorized
	}
	id := edgeEnrollmentIDFromPlaintext(token)
	if id == "" {
		return EdgeEnrollment{}, ErrUnauthorized
	}
	var row EdgeEnrollment
	var hash, created, revoked string
	err := s.db.QueryRow(`SELECT id, cluster, instance, prefix, hash, revoked_at, created_at
		FROM edge_enrollments WHERE id = ?`, id).Scan(&row.ID, &row.Cluster, &row.Instance, &row.Prefix, &hash, &revoked, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EdgeEnrollment{}, ErrUnauthorized
		}
		return EdgeEnrollment{}, err
	}
	if revoked != "" {
		return EdgeEnrollment{}, ErrUnauthorized
	}
	if row.Cluster != cluster || row.Instance != instance {
		return EdgeEnrollment{}, ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), tokenPassword(token)) != nil {
		return EdgeEnrollment{}, ErrUnauthorized
	}
	row.CreatedAt, _ = parseTime(created)
	return row, nil
}

func (s *GatewayStore) RevokeEdgeEnrollment(id string) error {
	res, err := s.db.Exec(`UPDATE edge_enrollments SET revoked_at = ? WHERE id = ? AND revoked_at = ''`,
		formatTime(time.Now().UTC()), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GatewayStore) ListEdgeEnrollments() ([]EdgeEnrollment, error) {
	rows, err := s.db.Query(`SELECT id, cluster, instance, prefix, revoked_at, created_at FROM edge_enrollments ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EdgeEnrollment
	for rows.Next() {
		var row EdgeEnrollment
		var revoked, created string
		if err := rows.Scan(&row.ID, &row.Cluster, &row.Instance, &row.Prefix, &revoked, &created); err != nil {
			return nil, err
		}
		row.Revoked = revoked != ""
		row.CreatedAt, _ = parseTime(created)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *GatewayStore) MarkEdgeAgentOnline(cluster, instance, remote string, sessions int, generation uint64) error {
	now := formatTime(time.Now().UTC())
	_, err := s.db.Exec(`INSERT INTO edge_agent_status
		(cluster, instance, status, sessions, remote, generation, connected_at, last_seen, updated_at)
		VALUES (?, ?, 'online', ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cluster, instance) DO UPDATE SET
			status = 'online', sessions = excluded.sessions, remote = excluded.remote,
			generation = excluded.generation, connected_at = excluded.connected_at,
			last_seen = excluded.last_seen, updated_at = excluded.updated_at`,
		cluster, instance, sessions, remote, generation, now, now, now)
	return err
}

func (s *GatewayStore) MarkEdgeAgentOffline(cluster, instance string, sessions int) error {
	now := formatTime(time.Now().UTC())
	_, err := s.db.Exec(`INSERT INTO edge_agent_status
		(cluster, instance, status, sessions, remote, generation, connected_at, last_seen, updated_at)
		VALUES (?, ?, 'offline', ?, '', 0, '', ?, ?)
		ON CONFLICT(cluster, instance) DO UPDATE SET
			status = 'offline', sessions = excluded.sessions, last_seen = excluded.last_seen,
			updated_at = excluded.updated_at`,
		cluster, instance, sessions, now, now)
	return err
}

func edgeEnrollmentIDFromPlaintext(token string) string {
	parts := strings.Split(strings.TrimSpace(token), "_")
	if len(parts) < 5 || parts[0] != "dge" || parts[1] != "een" || !strings.HasPrefix(parts[2]+"_"+parts[3], "een_") {
		return ""
	}
	// dge_een_een_<hex>_<secret> → id een_<hex>
	return "een_" + parts[3]
}
