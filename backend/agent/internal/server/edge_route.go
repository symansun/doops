package server

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Edge 路由把公网域名映射到某个 agent 名下的内网后端
//（docs/design/DOOPS_EDGE_TUNNEL.md §6.2、§6.3）。

const (
	EdgeRouteModePassthrough = "tls-passthrough"
	EdgeRouteModeTerminate   = "http-terminate"
)

const (
	edgeHostnameMaxBytes = 253
	edgeLabelMaxBytes    = 63
)

// EdgeRoute 是一条已落库的域名映射。
type EdgeRoute struct {
	ID        int64     `json:"id"`
	Hostname  string    `json:"hostname"`
	Cluster   string    `json:"cluster,omitempty"`
	Instance  string    `json:"instance,omitempty"`
	Backend   string    `json:"backend,omitempty"`
	Mode      string    `json:"mode"`
	Enabled   bool      `json:"enabled"`
	MaxConns  int       `json:"max_conns"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IsDeny 区分 deny 规则与转发规则。禁用且没有后端的路由只用来把某个 host 挡在公网外，
// 它靠"精确优先于通配"的匹配顺序覆盖同域的通配转发规则。
func (r EdgeRoute) IsDeny() bool {
	return !r.Enabled && strings.TrimSpace(r.Backend) == ""
}

type edgeRouteStatus string

const (
	edgeRouteAllowed     edgeRouteStatus = "allowed"
	edgeRouteDenied      edgeRouteStatus = "denied"
	edgeRouteMissing     edgeRouteStatus = "no-route"
	edgeRouteInvalidHost edgeRouteStatus = "invalid-host"
)

type edgeRouteDecision struct {
	Status  edgeRouteStatus
	Route   EdgeRoute
	Matched string
	Reason  string
}

// edgeHostname 是一个规范化后的域名模式。Canonical 是落库与展示用的形式，
// Key 是快照查表用的键：通配模式的 Key 去掉了 "*." 前缀，即父域本身。
type edgeHostname struct {
	Canonical string
	Key       string
	Wildcard  bool
}

func parseEdgeHostname(raw string) (edgeHostname, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" {
		return edgeHostname{}, errors.New("hostname is empty")
	}
	wildcard := false
	if rest, found := strings.CutPrefix(host, "*."); found {
		wildcard = true
		host = rest
	}
	if len(host) > edgeHostnameMaxBytes {
		return edgeHostname{}, fmt.Errorf("hostname %q exceeds %d bytes", raw, edgeHostnameMaxBytes)
	}
	labels := strings.Split(host, ".")
	if wildcard && len(labels) < 2 {
		return edgeHostname{}, fmt.Errorf("wildcard hostname %q needs a parent domain of at least two labels", raw)
	}
	for _, label := range labels {
		if err := validateEdgeHostLabel(label); err != nil {
			return edgeHostname{}, fmt.Errorf("hostname %q: %w", raw, err)
		}
	}
	canonical := host
	if wildcard {
		canonical = "*." + host
	}
	return edgeHostname{Canonical: canonical, Key: host, Wildcard: wildcard}, nil
}

// validateEdgeHostLabel 只接受 LDH 标签。SNI 里的国际化域名一定已是 punycode，
// 所以这里不做 IDN 转换，非 ASCII 直接判非法。
func validateEdgeHostLabel(label string) error {
	if label == "" {
		return errors.New("contains an empty label")
	}
	if len(label) > edgeLabelMaxBytes {
		return fmt.Errorf("label %q exceeds %d bytes", label, edgeLabelMaxBytes)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("label %q starts or ends with a hyphen", label)
	}
	for i := 0; i < len(label); i++ {
		switch char := label[i]; {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '-':
		default:
			return fmt.Errorf("label %q contains invalid character %q", label, rune(char))
		}
	}
	return nil
}

// normalizeEdgeBackend 规范化后端地址。Agent 侧的 -edge-backend-allow 按同一形式比对，
// 两边不一致会让白名单形同虚设。
func normalizeEdgeBackend(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("backend is empty")
	}
	host, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		return "", fmt.Errorf("backend %q must be host:port: %w", raw, err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("backend %q has an empty host", raw)
	}
	number, err := strconv.ParseUint(strings.TrimSpace(port), 10, 16)
	if err != nil || number == 0 {
		return "", fmt.Errorf("backend %q has an invalid port %q", raw, port)
	}
	return net.JoinHostPort(host, strconv.FormatUint(number, 10)), nil
}

func normalizeEdgeRouteMode(raw string) (string, error) {
	mode := strings.TrimSpace(strings.ToLower(raw))
	if mode == "" {
		return EdgeRouteModePassthrough, nil
	}
	switch mode {
	case EdgeRouteModePassthrough, EdgeRouteModeTerminate:
		return mode, nil
	}
	return "", fmt.Errorf("unsupported edge route mode %q", raw)
}

// edgeRouteSnapshot 是一份不可变的路由视图，由 EdgeRegistry 原子替换。
// 数据面只读快照，绝不在连接路径上查 SQLite——store 的连接池上限是 1，
// 在连接路径上查库会被串行化。
type edgeRouteSnapshot struct {
	version  int64
	exact    map[string]EdgeRoute
	wildcard map[string]EdgeRoute
}

func newEdgeRouteSnapshot(version int64, routes []EdgeRoute) (*edgeRouteSnapshot, error) {
	snapshot := &edgeRouteSnapshot{
		version:  version,
		exact:    make(map[string]EdgeRoute, len(routes)),
		wildcard: make(map[string]EdgeRoute),
	}
	for _, route := range routes {
		hostname, err := parseEdgeHostname(route.Hostname)
		if err != nil {
			return nil, fmt.Errorf("edge route %d: %w", route.ID, err)
		}
		table := snapshot.exact
		if hostname.Wildcard {
			table = snapshot.wildcard
		}
		if existing, duplicate := table[hostname.Key]; duplicate {
			return nil, fmt.Errorf("edge route %d duplicates hostname %s already held by route %d",
				route.ID, hostname.Canonical, existing.ID)
		}
		route.Hostname = hostname.Canonical
		table[hostname.Key] = route
	}
	return snapshot, nil
}

func (s *edgeRouteSnapshot) match(serverName string) edgeRouteDecision {
	hostname, err := parseEdgeHostname(serverName)
	if err != nil {
		return edgeRouteDecision{Status: edgeRouteInvalidHost, Reason: err.Error()}
	}
	if hostname.Wildcard {
		return edgeRouteDecision{Status: edgeRouteInvalidHost, Reason: "server name carries a wildcard label"}
	}
	if route, found := s.exact[hostname.Key]; found {
		return edgeRouteDecisionFor(route)
	}
	if parent, found := edgeParentDomain(hostname.Key); found {
		if route, found := s.wildcard[parent]; found {
			return edgeRouteDecisionFor(route)
		}
	}
	return edgeRouteDecision{Status: edgeRouteMissing, Reason: "no route matches " + hostname.Canonical}
}

func (s *edgeRouteSnapshot) byID(id int64) (EdgeRoute, bool) {
	if s == nil {
		return EdgeRoute{}, false
	}
	for _, route := range s.exact {
		if route.ID == id {
			return route, true
		}
	}
	for _, route := range s.wildcard {
		if route.ID == id {
			return route, true
		}
	}
	return EdgeRoute{}, false
}

func (s *edgeRouteSnapshot) forTarget(cluster, instance string) []EdgeRoute {
	if s == nil {
		return nil
	}
	var routes []EdgeRoute
	appendMatch := func(route EdgeRoute) {
		if route.Cluster == cluster && route.Instance == instance {
			routes = append(routes, route)
		}
	}
	for _, route := range s.exact {
		appendMatch(route)
	}
	for _, route := range s.wildcard {
		appendMatch(route)
	}
	return routes
}

func edgeRouteDecisionFor(route EdgeRoute) edgeRouteDecision {
	decision := edgeRouteDecision{Route: route, Matched: route.Hostname, Status: edgeRouteAllowed}
	if !route.Enabled {
		decision.Status = edgeRouteDenied
		decision.Reason = "route " + route.Hostname + " is disabled"
	}
	return decision
}

// edgeParentDomain 去掉最左一级标签。单级通配语义与 TLS 通配证书一致：
// *.jm.example.com 匹配 a.jm.example.com，不匹配 a.b.jm.example.com。
func edgeParentDomain(host string) (string, bool) {
	dot := strings.IndexByte(host, '.')
	if dot < 0 || dot+1 >= len(host) {
		return "", false
	}
	return host[dot+1:], true
}
