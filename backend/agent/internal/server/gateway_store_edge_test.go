package server

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateEdgeRouteNormalizesAndPersists(t *testing.T) {
	store := openEdgeTestStore(t)
	route, err := store.CreateEdgeRoute(CreateEdgeRouteRequest{
		Hostname: "  AMP.JM.Example.COM.  ",
		Cluster:  " jm ",
		Instance: " gw-jm ",
		Backend:  " 192.168.150.228:443 ",
		Note:     " JM AMP 公网入口 ",
	})
	if err != nil {
		t.Fatalf("create edge route: %v", err)
	}
	if route.Hostname != "amp.jm.example.com" {
		t.Fatalf("hostname = %q, want it canonicalized", route.Hostname)
	}
	if route.Cluster != "jm" || route.Instance != "gw-jm" || route.Backend != "192.168.150.228:443" {
		t.Fatalf("route = %+v, want trimmed cluster, instance and backend", route)
	}
	if route.Mode != EdgeRouteModePassthrough {
		t.Fatalf("mode = %q, want %q by default", route.Mode, EdgeRouteModePassthrough)
	}
	if !route.Enabled || route.IsDeny() {
		t.Fatalf("route = %+v, want an enabled forwarding route", route)
	}
	if route.CreatedAt.IsZero() || route.UpdatedAt.IsZero() {
		t.Fatalf("route timestamps = %v / %v, want both set", route.CreatedAt, route.UpdatedAt)
	}
	routes, err := store.ListEdgeRoutes()
	if err != nil {
		t.Fatalf("list edge routes: %v", err)
	}
	if len(routes) != 1 || routes[0].ID != route.ID {
		t.Fatalf("listed %d routes, want the one just created", len(routes))
	}
}

func TestCreateEdgeRouteAcceptsDenyRuleWithoutBackend(t *testing.T) {
	store := openEdgeTestStore(t)
	route, err := store.CreateEdgeRoute(CreateEdgeRouteRequest{
		Hostname: "grafana.jm.example.com",
		Disabled: true,
		Note:     "仅运维",
	})
	if err != nil {
		t.Fatalf("create deny route: %v", err)
	}
	if !route.IsDeny() {
		t.Fatalf("route = %+v, want a deny rule", route)
	}
}

func TestCreateEdgeRouteRejectsIncompleteInput(t *testing.T) {
	store := openEdgeTestStore(t)
	cases := []struct {
		name    string
		request CreateEdgeRouteRequest
		want    string
	}{
		{
			"enabled without backend",
			CreateEdgeRouteRequest{Hostname: "amp.jm.example.com", Cluster: "jm", Instance: "gw-jm"},
			"an enabled edge route requires a backend",
		},
		{
			"forwarding without target",
			CreateEdgeRouteRequest{Hostname: "amp.jm.example.com", Backend: "192.168.150.228:443"},
			"requires both cluster and instance",
		},
		{
			"deny naming a target",
			CreateEdgeRouteRequest{Hostname: "grafana.jm.example.com", Cluster: "jm", Disabled: true},
			"must not name a cluster or instance",
		},
		{
			"invalid hostname",
			CreateEdgeRouteRequest{Hostname: "amp_1.jm.example.com", Cluster: "jm", Instance: "gw-jm", Backend: "192.168.150.228:443"},
			"invalid character",
		},
		{
			"invalid backend",
			CreateEdgeRouteRequest{Hostname: "amp.jm.example.com", Cluster: "jm", Instance: "gw-jm", Backend: "192.168.150.228"},
			"must be host:port",
		},
		{
			"invalid mode",
			CreateEdgeRouteRequest{Hostname: "amp.jm.example.com", Cluster: "jm", Instance: "gw-jm", Backend: "192.168.150.228:443", Mode: "socks5"},
			"unsupported edge route mode",
		},
		{
			"negative max conns",
			CreateEdgeRouteRequest{Hostname: "amp.jm.example.com", Cluster: "jm", Instance: "gw-jm", Backend: "192.168.150.228:443", MaxConns: -1},
			"must not be negative",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := store.CreateEdgeRoute(testCase.request); err == nil {
				t.Fatalf("create accepted %s", testCase.name)
			} else if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("create error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

func TestCreateEdgeRouteRejectsDuplicateHostname(t *testing.T) {
	store := openEdgeTestStore(t)
	request := CreateEdgeRouteRequest{
		Hostname: "amp.jm.example.com",
		Cluster:  "jm",
		Instance: "gw-jm",
		Backend:  "192.168.150.228:443",
	}
	if _, err := store.CreateEdgeRoute(request); err != nil {
		t.Fatalf("create first route: %v", err)
	}
	request.Hostname = "AMP.jm.example.com."
	_, err := store.CreateEdgeRoute(request)
	if !errors.Is(err, ErrEdgeRouteExists) {
		t.Fatalf("create error = %v, want ErrEdgeRouteExists", err)
	}
}

func TestUpdateEdgeRouteRevalidatesMergedResult(t *testing.T) {
	store := openEdgeTestStore(t)
	deny, err := store.CreateEdgeRoute(CreateEdgeRouteRequest{Hostname: "grafana.jm.example.com", Disabled: true})
	if err != nil {
		t.Fatalf("create deny route: %v", err)
	}

	enabled := true
	if _, err := store.UpdateEdgeRoute(deny.ID, UpdateEdgeRouteRequest{Enabled: &enabled}); err == nil {
		t.Fatal("update enabled a route that still has no backend")
	}

	cluster, instance, backend := "jm", "gw-jm", "192.168.150.228:443"
	promoted, err := store.UpdateEdgeRoute(deny.ID, UpdateEdgeRouteRequest{
		Cluster:  &cluster,
		Instance: &instance,
		Backend:  &backend,
		Enabled:  &enabled,
	})
	if err != nil {
		t.Fatalf("promote deny route to forwarding: %v", err)
	}
	if !promoted.Enabled || promoted.Backend != backend || promoted.IsDeny() {
		t.Fatalf("promoted route = %+v, want an enabled forwarding route", promoted)
	}
	if promoted.Hostname != "grafana.jm.example.com" {
		t.Fatalf("hostname = %q, want it unchanged by an update", promoted.Hostname)
	}
}

func TestUpdateAndDeleteEdgeRouteReportMissingRows(t *testing.T) {
	store := openEdgeTestStore(t)
	note := "missing"
	if _, err := store.UpdateEdgeRoute(404, UpdateEdgeRouteRequest{Note: &note}); !errors.Is(err, ErrEdgeRouteNotFound) {
		t.Fatalf("update error = %v, want ErrEdgeRouteNotFound", err)
	}
	if err := store.DeleteEdgeRoute(404); !errors.Is(err, ErrEdgeRouteNotFound) {
		t.Fatalf("delete error = %v, want ErrEdgeRouteNotFound", err)
	}
}

// 每次写路由都要推进修订号，agent 才能靠 ROUTES 帧的 version 判断自己的视图是否过期。
func TestEdgeRouteRevisionAdvancesOnEveryWrite(t *testing.T) {
	store := openEdgeTestStore(t)
	initial := mustLoadEdgeSnapshot(t, store)
	if initial.version <= 0 {
		t.Fatalf("initial version = %d, want a positive revision", initial.version)
	}

	route, err := store.CreateEdgeRoute(CreateEdgeRouteRequest{
		Hostname: "amp.jm.example.com",
		Cluster:  "jm",
		Instance: "gw-jm",
		Backend:  "192.168.150.228:443",
	})
	if err != nil {
		t.Fatalf("create edge route: %v", err)
	}
	afterCreate := mustLoadEdgeSnapshot(t, store)
	if afterCreate.version <= initial.version {
		t.Fatalf("version after create = %d, want it above %d", afterCreate.version, initial.version)
	}

	disabled := false
	if _, err := store.UpdateEdgeRoute(route.ID, UpdateEdgeRouteRequest{Enabled: &disabled}); err != nil {
		t.Fatalf("disable edge route: %v", err)
	}
	afterUpdate := mustLoadEdgeSnapshot(t, store)
	if afterUpdate.version <= afterCreate.version {
		t.Fatalf("version after update = %d, want it above %d", afterUpdate.version, afterCreate.version)
	}

	if err := store.DeleteEdgeRoute(route.ID); err != nil {
		t.Fatalf("delete edge route: %v", err)
	}
	afterDelete := mustLoadEdgeSnapshot(t, store)
	if afterDelete.version <= afterUpdate.version {
		t.Fatalf("version after delete = %d, want it above %d", afterDelete.version, afterUpdate.version)
	}
	if len(afterDelete.exact) != 0 || len(afterDelete.wildcard) != 0 {
		t.Fatalf("snapshot still holds %d exact and %d wildcard routes", len(afterDelete.exact), len(afterDelete.wildcard))
	}
}

// 端到端复现 §12 的 JM 落地顺序：先落 deny 清单，再开通配转发。
func TestLoadEdgeRouteSnapshotServesJMRoutingPlan(t *testing.T) {
	store := openEdgeTestStore(t)
	for _, host := range []string{"grafana.jm.example.com", "harbor.jm.example.com"} {
		if _, err := store.CreateEdgeRoute(CreateEdgeRouteRequest{Hostname: host, Disabled: true, Note: "仅运维"}); err != nil {
			t.Fatalf("create deny route %s: %v", host, err)
		}
	}
	for _, host := range []string{"jm.example.com", "*.jm.example.com"} {
		if _, err := store.CreateEdgeRoute(CreateEdgeRouteRequest{
			Hostname: host,
			Cluster:  "jm",
			Instance: "gw-jm",
			Backend:  "192.168.150.228:443",
		}); err != nil {
			t.Fatalf("create forwarding route %s: %v", host, err)
		}
	}

	snapshot := mustLoadEdgeSnapshot(t, store)
	for host, want := range map[string]edgeRouteStatus{
		"jm.example.com":         edgeRouteAllowed,
		"amp.jm.example.com":     edgeRouteAllowed,
		"webide.jm.example.com":  edgeRouteAllowed,
		"grafana.jm.example.com": edgeRouteDenied,
		"harbor.jm.example.com":  edgeRouteDenied,
		"a.b.jm.example.com":     edgeRouteMissing,
		"amp.oilan.example.com":  edgeRouteMissing,
	} {
		if got := snapshot.match(host).Status; got != want {
			t.Fatalf("match %s = %s, want %s", host, got, want)
		}
	}
	if backend := snapshot.match("amp.jm.example.com").Route.Backend; backend != "192.168.150.228:443" {
		t.Fatalf("amp backend = %q, want the JM ingress", backend)
	}
}

func openEdgeTestStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustLoadEdgeSnapshot(t *testing.T, store *GatewayStore) *edgeRouteSnapshot {
	t.Helper()
	snapshot, err := store.LoadEdgeRouteSnapshot()
	if err != nil {
		t.Fatalf("load edge route snapshot: %v", err)
	}
	return snapshot
}
