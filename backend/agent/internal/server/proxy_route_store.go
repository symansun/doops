package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrProxyRouteHostInUse = errors.New("proxy host already in use")
	ErrProxyRouteStale     = errors.New("proxy route changed since it was read")
)

type ProxyRoute struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	Cluster   string    `json:"cluster"`
	Instance  string    `json:"instance"`
	Upstream  string    `json:"upstream"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ProxyRouteInput struct {
	ID       string
	Host     string
	Cluster  string
	Instance string
	Upstream string
	Enabled  bool
	// ExpectUpdatedAt, when set on an update, rejects the write with
	// ErrProxyRouteStale unless the stored route still has this updated_at.
	ExpectUpdatedAt *time.Time
}

func normalizeProxyHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("proxy host is required")
	}
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "/?#") {
		return "", fmt.Errorf("proxy host must be a hostname without scheme or path")
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		if port == "" {
			return "", fmt.Errorf("proxy host port is invalid")
		}
		raw = host
	} else if strings.Contains(raw, ":") {
		return "", fmt.Errorf("proxy host must not contain a port")
	}
	raw = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if raw == "" || len(raw) > 253 || strings.ContainsAny(raw, " \t\r\n") {
		return "", fmt.Errorf("proxy host is invalid")
	}
	if ip := net.ParseIP(raw); ip != nil {
		return raw, nil
	}
	for _, label := range strings.Split(raw, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("proxy host is invalid")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return "", fmt.Errorf("proxy host is invalid")
			}
		}
	}
	return raw, nil
}

func normalizeProxyUpstream(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("proxy upstream must be an http(s) URL without credentials, query, or fragment")
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

func normalizeProxyRouteInput(input ProxyRouteInput, requireID bool) (ProxyRouteInput, error) {
	host, err := normalizeProxyHost(input.Host)
	if err != nil {
		return ProxyRouteInput{}, err
	}
	upstream, err := normalizeProxyUpstream(input.Upstream)
	if err != nil {
		return ProxyRouteInput{}, err
	}
	if strings.TrimSpace(input.Cluster) == "" || strings.TrimSpace(input.Instance) == "" {
		return ProxyRouteInput{}, fmt.Errorf("proxy route cluster and instance are required")
	}
	input.ID = strings.TrimSpace(input.ID)
	if requireID && input.ID == "" {
		return ProxyRouteInput{}, fmt.Errorf("proxy route id is required")
	}
	input.Host = host
	input.Cluster = strings.TrimSpace(input.Cluster)
	input.Instance = strings.TrimSpace(input.Instance)
	input.Upstream = upstream
	return input, nil
}

func (s *GatewayStore) ListProxyRoutes() ([]ProxyRoute, error) {
	rows, err := s.db.Query(`SELECT id, host, cluster, instance, upstream, enabled, created_at, updated_at
		FROM proxy_routes ORDER BY host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []ProxyRoute
	for rows.Next() {
		var route ProxyRoute
		var enabled int
		var created, updated string
		if err := rows.Scan(&route.ID, &route.Host, &route.Cluster, &route.Instance, &route.Upstream, &enabled, &created, &updated); err != nil {
			return nil, err
		}
		route.Enabled = enabled != 0
		route.CreatedAt, _ = parseTime(created)
		route.UpdatedAt, _ = parseTime(updated)
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (s *GatewayStore) ProxyRouteByHost(host string) (ProxyRoute, error) {
	host, err := normalizeProxyHost(host)
	if err != nil {
		return ProxyRoute{}, err
	}
	var route ProxyRoute
	var enabled int
	var created, updated string
	err = s.db.QueryRow(`SELECT id, host, cluster, instance, upstream, enabled, created_at, updated_at
		FROM proxy_routes WHERE host = ?`, host).
		Scan(&route.ID, &route.Host, &route.Cluster, &route.Instance, &route.Upstream, &enabled, &created, &updated)
	if err != nil {
		return ProxyRoute{}, err
	}
	route.Enabled = enabled != 0
	route.CreatedAt, _ = parseTime(created)
	route.UpdatedAt, _ = parseTime(updated)
	return route, nil
}

// UpsertProxyRoute creates a route when input.ID is empty and otherwise updates
// that route in place. An update never recreates a deleted route (it returns
// sql.ErrNoRows), and a host another route already serves is
// ErrProxyRouteHostInUse.
func (s *GatewayStore) UpsertProxyRoute(input ProxyRouteInput) (ProxyRoute, error) {
	input, err := normalizeProxyRouteInput(input, false)
	if err != nil {
		return ProxyRoute{}, err
	}
	route := ProxyRoute{ID: input.ID, Host: input.Host, Cluster: input.Cluster, Instance: input.Instance,
		Upstream: input.Upstream, Enabled: input.Enabled}
	tx, err := s.db.Begin()
	if err != nil {
		return ProxyRoute{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if route.ID == "" {
		route.ID = "route_" + randomHex(12)
		route.CreatedAt, route.UpdatedAt = now, now
		_, err = tx.Exec(`INSERT INTO proxy_routes(id, host, cluster, instance, upstream, enabled, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			route.ID, route.Host, route.Cluster, route.Instance, route.Upstream, boolInt(route.Enabled),
			formatTime(now), formatTime(now))
	} else {
		var created, updated string
		if err := tx.QueryRow(`SELECT created_at, updated_at FROM proxy_routes WHERE id = ?`, route.ID).
			Scan(&created, &updated); err != nil {
			return ProxyRoute{}, err
		}
		route.CreatedAt, _ = parseTime(created)
		previous, _ := parseTime(updated)
		if input.ExpectUpdatedAt != nil && !input.ExpectUpdatedAt.Equal(previous) {
			return ProxyRoute{}, ErrProxyRouteStale
		}
		// updated_at is the version clients send back, so every write must move it.
		if !now.After(previous) {
			now = previous.Add(time.Nanosecond)
		}
		route.UpdatedAt = now
		_, err = tx.Exec(`UPDATE proxy_routes SET host = ?, cluster = ?, instance = ?, upstream = ?, enabled = ?, updated_at = ?
			WHERE id = ?`,
			route.Host, route.Cluster, route.Instance, route.Upstream, boolInt(route.Enabled), formatTime(now), route.ID)
	}
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return ProxyRoute{}, ErrProxyRouteHostInUse
		}
		return ProxyRoute{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProxyRoute{}, err
	}
	return route, nil
}

func (s *GatewayStore) DeleteProxyRoute(id string) error {
	res, err := s.db.Exec(`DELETE FROM proxy_routes WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
