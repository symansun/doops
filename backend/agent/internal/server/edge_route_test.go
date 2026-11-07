package server

import (
	"strings"
	"testing"
)

func TestParseEdgeHostnameCanonicalizesInput(t *testing.T) {
	cases := []struct {
		raw          string
		canonical    string
		key          string
		wantWildcard bool
	}{
		{"amp.jm.aiedulab.cn", "amp.jm.aiedulab.cn", "amp.jm.aiedulab.cn", false},
		{"  AMP.JM.AieduLab.CN.  ", "amp.jm.aiedulab.cn", "amp.jm.aiedulab.cn", false},
		{"*.jm.aiedulab.cn", "*.jm.aiedulab.cn", "jm.aiedulab.cn", true},
		{"*.JM.aiedulab.cn.", "*.jm.aiedulab.cn", "jm.aiedulab.cn", true},
		{"xn--fiq228c.cn", "xn--fiq228c.cn", "xn--fiq228c.cn", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.raw, func(t *testing.T) {
			hostname, err := parseEdgeHostname(testCase.raw)
			if err != nil {
				t.Fatalf("parse %q: %v", testCase.raw, err)
			}
			if hostname.Canonical != testCase.canonical {
				t.Fatalf("canonical = %q, want %q", hostname.Canonical, testCase.canonical)
			}
			if hostname.Key != testCase.key {
				t.Fatalf("key = %q, want %q", hostname.Key, testCase.key)
			}
			if hostname.Wildcard != testCase.wantWildcard {
				t.Fatalf("wildcard = %v, want %v", hostname.Wildcard, testCase.wantWildcard)
			}
		})
	}
}

func TestParseEdgeHostnameRejectsInvalidPatterns(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "   ", "hostname is empty"},
		{"wildcard without parent", "*.cn", "needs a parent domain of at least two labels"},
		{"embedded wildcard", "a.*.jm.aiedulab.cn", "invalid character"},
		{"underscore", "amp_1.jm.aiedulab.cn", "invalid character"},
		{"non ascii", "证书.aiedulab.cn", "invalid character"},
		{"empty label", "amp..aiedulab.cn", "contains an empty label"},
		{"leading hyphen", "-amp.aiedulab.cn", "starts or ends with a hyphen"},
		{"overlong label", strings.Repeat("a", 64) + ".aiedulab.cn", "exceeds 63 bytes"},
		{"overlong hostname", strings.Repeat("a.", 130) + "cn", "exceeds 253 bytes"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := parseEdgeHostname(testCase.raw); err == nil {
				t.Fatalf("parse accepted %s", testCase.name)
			} else if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("parse error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

func TestEdgeRouteSnapshotPrefersExactOverWildcard(t *testing.T) {
	snapshot := mustEdgeSnapshot(t, 7,
		edgeForwardRoute(1, "*.jm.aiedulab.cn"),
		edgeForwardRoute(2, "amp.jm.aiedulab.cn"),
	)
	decision := snapshot.match("amp.jm.aiedulab.cn")
	if decision.Status != edgeRouteAllowed {
		t.Fatalf("status = %s, want %s", decision.Status, edgeRouteAllowed)
	}
	if decision.Route.ID != 2 {
		t.Fatalf("matched route %d, want the exact route 2", decision.Route.ID)
	}
	if decision.Matched != "amp.jm.aiedulab.cn" {
		t.Fatalf("matched pattern = %q, want the exact hostname", decision.Matched)
	}
}

// 禁用的精确路由就是 §7.3 的 deny 规则：它必须盖住同域的通配转发路由。
func TestEdgeRouteSnapshotDeniesViaDisabledExactRoute(t *testing.T) {
	snapshot := mustEdgeSnapshot(t, 1,
		edgeForwardRoute(1, "*.jm.aiedulab.cn"),
		EdgeRoute{ID: 2, Hostname: "grafana.jm.aiedulab.cn", Mode: EdgeRouteModePassthrough},
	)
	denied := snapshot.match("grafana.jm.aiedulab.cn")
	if denied.Status != edgeRouteDenied {
		t.Fatalf("grafana status = %s, want %s", denied.Status, edgeRouteDenied)
	}
	if !denied.Route.IsDeny() {
		t.Fatalf("route %+v should be recognised as a deny rule", denied.Route)
	}
	allowed := snapshot.match("amp.jm.aiedulab.cn")
	if allowed.Status != edgeRouteAllowed || allowed.Route.ID != 1 {
		t.Fatalf("amp decision = %+v, want the wildcard route to still forward", allowed)
	}
}

func TestEdgeRouteSnapshotWildcardMatchesOneLabelOnly(t *testing.T) {
	snapshot := mustEdgeSnapshot(t, 1, edgeForwardRoute(1, "*.jm.aiedulab.cn"))
	if decision := snapshot.match("a.jm.aiedulab.cn"); decision.Status != edgeRouteAllowed {
		t.Fatalf("single label status = %s, want %s", decision.Status, edgeRouteAllowed)
	}
	if decision := snapshot.match("a.b.jm.aiedulab.cn"); decision.Status != edgeRouteMissing {
		t.Fatalf("two label status = %s, want %s", decision.Status, edgeRouteMissing)
	}
	// 通配路由不覆盖父域本身，与 TLS 通配证书一致。
	if decision := snapshot.match("jm.aiedulab.cn"); decision.Status != edgeRouteMissing {
		t.Fatalf("parent domain status = %s, want %s", decision.Status, edgeRouteMissing)
	}
}

func TestEdgeRouteSnapshotRejectsUnknownAndInvalidServerNames(t *testing.T) {
	snapshot := mustEdgeSnapshot(t, 1, edgeForwardRoute(1, "amp.jm.aiedulab.cn"))
	if decision := snapshot.match("harbor.oilan.aiedulab.cn"); decision.Status != edgeRouteMissing {
		t.Fatalf("unknown host status = %s, want %s", decision.Status, edgeRouteMissing)
	}
	for _, serverName := range []string{"", "   ", "amp_1.jm.aiedulab.cn", "*.jm.aiedulab.cn", "证书.aiedulab.cn"} {
		if decision := snapshot.match(serverName); decision.Status != edgeRouteInvalidHost {
			t.Fatalf("server name %q status = %s, want %s", serverName, decision.Status, edgeRouteInvalidHost)
		}
	}
}

func TestNewEdgeRouteSnapshotRejectsDuplicateHostnames(t *testing.T) {
	_, err := newEdgeRouteSnapshot(1,
		[]EdgeRoute{edgeForwardRoute(1, "amp.jm.aiedulab.cn"), edgeForwardRoute(2, "AMP.jm.aiedulab.cn.")})
	if err == nil || !strings.Contains(err.Error(), "duplicates hostname") {
		t.Fatalf("snapshot error = %v, want a duplicate hostname rejection", err)
	}
	// 精确与通配是两张表，同名父域不算冲突。
	if _, err := newEdgeRouteSnapshot(1,
		[]EdgeRoute{edgeForwardRoute(1, "jm.aiedulab.cn"), edgeForwardRoute(2, "*.jm.aiedulab.cn")}); err != nil {
		t.Fatalf("snapshot rejected an exact and wildcard pair on the same parent: %v", err)
	}
}

func TestEdgeRouteSnapshotKeepsVersion(t *testing.T) {
	snapshot := mustEdgeSnapshot(t, 42, edgeForwardRoute(1, "amp.jm.aiedulab.cn"))
	if snapshot.version != 42 {
		t.Fatalf("version = %d, want 42", snapshot.version)
	}
}

func TestNormalizeEdgeBackend(t *testing.T) {
	for raw, want := range map[string]string{
		" 192.168.150.228:443 ": "192.168.150.228:443",
		"ingress.jm.svc:8443":   "ingress.jm.svc:8443",
		"[2001:db8::1]:443":     "[2001:db8::1]:443",
	} {
		got, err := normalizeEdgeBackend(raw)
		if err != nil {
			t.Fatalf("normalize %q: %v", raw, err)
		}
		if got != want {
			t.Fatalf("normalize %q = %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{"", "192.168.150.228", "192.168.150.228:0", "192.168.150.228:https", ":443", "192.168.150.228:99999"} {
		if _, err := normalizeEdgeBackend(raw); err == nil {
			t.Fatalf("normalize accepted %q", raw)
		}
	}
}

func TestNormalizeEdgeRouteMode(t *testing.T) {
	if mode, err := normalizeEdgeRouteMode(""); err != nil || mode != EdgeRouteModePassthrough {
		t.Fatalf("empty mode = %q (err %v), want %q", mode, err, EdgeRouteModePassthrough)
	}
	if mode, err := normalizeEdgeRouteMode(" HTTP-Terminate "); err != nil || mode != EdgeRouteModeTerminate {
		t.Fatalf("mode = %q (err %v), want %q", mode, err, EdgeRouteModeTerminate)
	}
	if _, err := normalizeEdgeRouteMode("socks5"); err == nil {
		t.Fatal("normalize accepted an unsupported mode")
	}
}

func mustEdgeSnapshot(t *testing.T, version int64, routes ...EdgeRoute) *edgeRouteSnapshot {
	t.Helper()
	snapshot, err := newEdgeRouteSnapshot(version, routes)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	return snapshot
}

func edgeForwardRoute(id int64, hostname string) EdgeRoute {
	return EdgeRoute{
		ID:       id,
		Hostname: hostname,
		Cluster:  "jm",
		Instance: "gw-jm",
		Backend:  "192.168.150.228:443",
		Mode:     EdgeRouteModePassthrough,
		Enabled:  true,
	}
}
