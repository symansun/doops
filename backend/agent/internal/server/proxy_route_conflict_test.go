package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

type proxyRouteConflictSeed struct {
	store *GatewayStore
	a, b  ProxyRoute
}

func seedProxyRouteConflict(t *testing.T) proxyRouteConflictSeed {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	seed := proxyRouteConflictSeed{store: store}
	for _, target := range []struct {
		route *ProxyRoute
		host  string
	}{{&seed.a, "app.example.test"}, {&seed.b, "other.example.test"}} {
		*target.route, err = store.UpsertProxyRoute(ProxyRouteInput{
			Host: target.host, Cluster: "dev", Instance: "node", Upstream: "http://127.0.0.1:8080", Enabled: true,
		})
		if err != nil {
			t.Fatalf("seed %s: %v", target.host, err)
		}
	}
	return seed
}

func proxyRouteEdit(route ProxyRoute, upstream string) ProxyRouteInput {
	return ProxyRouteInput{ID: route.ID, Host: route.Host, Cluster: route.Cluster, Instance: route.Instance,
		Upstream: upstream, Enabled: route.Enabled}
}

func proxyRoutesJSON(t *testing.T, store *GatewayStore) string {
	t.Helper()
	routes, err := store.ListProxyRoutes()
	if err != nil {
		t.Fatalf("list proxy routes: %v", err)
	}
	raw, err := json.Marshal(routes)
	if err != nil {
		t.Fatalf("marshal proxy routes: %v", err)
	}
	return string(raw)
}

// Another admin's write between this client's read and its save.
func concurrentProxyRouteEdit(t *testing.T, s proxyRouteConflictSeed) {
	t.Helper()
	if _, err := s.store.UpsertProxyRoute(proxyRouteEdit(s.a, "http://127.0.0.1:7070")); err != nil {
		t.Fatalf("concurrent edit: %v", err)
	}
}

func TestUpsertProxyRouteConflicts(t *testing.T) {
	cases := []struct {
		name  string
		input func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput
		want  error
	}{
		{
			name: "new route reuses a host",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				return ProxyRouteInput{Host: "app.example.test", Cluster: "dev", Instance: "node", Upstream: "http://127.0.0.1:9090", Enabled: true}
			},
			want: ErrProxyRouteHostInUse,
		},
		{
			name: "new route reuses a host in other case",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				return ProxyRouteInput{Host: "APP.Example.Test", Cluster: "dev", Instance: "node", Upstream: "http://127.0.0.1:9090", Enabled: true}
			},
			want: ErrProxyRouteHostInUse,
		},
		{
			name: "new route reuses a host with a trailing dot",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				return ProxyRouteInput{Host: "app.example.test.", Cluster: "dev", Instance: "node", Upstream: "http://127.0.0.1:9090", Enabled: true}
			},
			want: ErrProxyRouteHostInUse,
		},
		{
			name: "update renames onto another route's host",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				input := proxyRouteEdit(s.b, s.b.Upstream)
				input.Host = "App.Example.Test."
				return input
			},
			want: ErrProxyRouteHostInUse,
		},
		{
			name: "update after the route was deleted",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				if err := s.store.DeleteProxyRoute(s.a.ID); err != nil {
					t.Fatalf("delete: %v", err)
				}
				return proxyRouteEdit(s.a, "http://127.0.0.1:9090")
			},
			want: sql.ErrNoRows,
		},
		{
			name: "update with a version after the route was deleted",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				if err := s.store.DeleteProxyRoute(s.a.ID); err != nil {
					t.Fatalf("delete: %v", err)
				}
				input := proxyRouteEdit(s.a, "http://127.0.0.1:9090")
				input.ExpectUpdatedAt = &s.a.UpdatedAt
				return input
			},
			want: sql.ErrNoRows,
		},
		{
			name: "update an id that never existed",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				input := proxyRouteEdit(s.a, "http://127.0.0.1:9090")
				input.ID, input.Host = "route_000000000000000000000000", "fresh.example.test"
				return input
			},
			want: sql.ErrNoRows,
		},
		{
			name: "stale version",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				concurrentProxyRouteEdit(t, s)
				input := proxyRouteEdit(s.a, "http://127.0.0.1:9090")
				input.ExpectUpdatedAt = &s.a.UpdatedAt
				return input
			},
			want: ErrProxyRouteStale,
		},
		{
			name: "version from before the route was created",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				input := proxyRouteEdit(s.a, "http://127.0.0.1:9090")
				earlier := s.a.UpdatedAt.Add(-time.Second)
				input.ExpectUpdatedAt = &earlier
				return input
			},
			want: ErrProxyRouteStale,
		},
		{
			name: "current version updates in place",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				input := proxyRouteEdit(s.a, "http://127.0.0.1:9090")
				input.ExpectUpdatedAt = &s.a.UpdatedAt
				return input
			},
		},
		{
			name: "client without a version still overwrites",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				concurrentProxyRouteEdit(t, s)
				return proxyRouteEdit(s.a, "http://127.0.0.1:9090")
			},
		},
		{
			name: "update keeps its own host written in other case",
			input: func(t *testing.T, s proxyRouteConflictSeed) ProxyRouteInput {
				input := proxyRouteEdit(s.a, "http://127.0.0.1:9090")
				input.Host = "APP.example.test."
				return input
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := seedProxyRouteConflict(t)
			input := tc.input(t, s)
			before := proxyRoutesJSON(t, s.store)
			got, err := s.store.UpsertProxyRoute(input)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				if after := proxyRoutesJSON(t, s.store); after != before {
					t.Fatalf("rejected write changed routes:\nbefore %s\nafter  %s", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if got.ID != s.a.ID || got.Host != s.a.Host || got.Upstream != "http://127.0.0.1:9090/" ||
				!got.CreatedAt.Equal(s.a.CreatedAt) || !got.UpdatedAt.After(s.a.UpdatedAt) {
				t.Fatalf("updated route = %#v, seeded %#v", got, s.a)
			}
			stored, err := s.store.ProxyRouteByHost(got.Host)
			if err != nil {
				t.Fatalf("reread: %v", err)
			}
			if storedJSON, _ := json.Marshal(stored); string(storedJSON) != string(mustMarshalProxyRoute(t, got)) {
				t.Fatalf("returned route %s differs from stored %s", mustMarshalProxyRoute(t, got), storedJSON)
			}
		})
	}
}

func mustMarshalProxyRoute(t *testing.T, route ProxyRoute) []byte {
	t.Helper()
	raw, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("marshal route: %v", err)
	}
	return raw
}

// proxyRouteWire keeps updated_at as the exact string the Gateway sent, the
// way the CLI and the Web console send it back.
type proxyRouteWire struct {
	ID        string `json:"id"`
	Host      string `json:"host"`
	Cluster   string `json:"cluster"`
	Instance  string `json:"instance"`
	Upstream  string `json:"upstream"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at"`
}

type proxyRouteHTTPFixture struct {
	mux   *http.ServeMux
	token string
	a, b  proxyRouteWire
}

func newProxyRouteHTTPFixture(t *testing.T) *proxyRouteHTTPFixture {
	t.Helper()
	store, token := newAdminStoreAndToken(t)
	t.Cleanup(func() { store.Close() })
	f := &proxyRouteHTTPFixture{mux: http.NewServeMux(), token: token}
	NewGatewayHub(store, GatewayHubOptions{}).RegisterRoutes(f.mux)
	for _, target := range []struct {
		route *proxyRouteWire
		host  string
	}{{&f.a, "app.example.test"}, {&f.b, "other.example.test"}} {
		*target.route = f.post(t, map[string]any{
			"host": target.host, "cluster": "dev", "instance": "node", "upstream": "http://127.0.0.1:8080",
		}, http.StatusOK)
	}
	return f
}

func (f *proxyRouteHTTPFixture) post(t *testing.T, body map[string]any, wantCode int) proxyRouteWire {
	t.Helper()
	response := credentialHTTPRequest(t, f.mux, f.token, http.MethodPost, "/v1/admin/proxy-routes", body)
	if response.Code != wantCode {
		t.Fatalf("POST HTTP %d, want %d: %s", response.Code, wantCode, response.Body.String())
	}
	var route proxyRouteWire
	if wantCode == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &route); err != nil {
			t.Fatalf("decode route: %v", err)
		}
	}
	return route
}

func (f *proxyRouteHTTPFixture) list(t *testing.T) string {
	t.Helper()
	response := credentialHTTPRequest(t, f.mux, f.token, http.MethodGet, "/v1/admin/proxy-routes", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET HTTP %d: %s", response.Code, response.Body.String())
	}
	return response.Body.String()
}

func proxyRouteEditBody(route proxyRouteWire, upstream string) map[string]any {
	return map[string]any{
		"id": route.ID, "host": route.Host, "cluster": route.Cluster, "instance": route.Instance,
		"upstream": upstream, "enabled": route.Enabled,
	}
}

func TestAdminProxyRoutesHTTPConflicts(t *testing.T) {
	cases := []struct {
		name     string
		body     func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any
		wantCode int
		wantBody string
	}{
		{
			name: "new route reuses a host",
			body: func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any {
				return map[string]any{"host": "app.example.test", "cluster": "dev", "instance": "node", "upstream": "http://127.0.0.1:9090"}
			},
			wantCode: http.StatusConflict, wantBody: "proxy host already in use",
		},
		{
			name: "new route reuses a host in other case with a trailing dot",
			body: func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any {
				return map[string]any{"host": "APP.Example.Test.", "cluster": "dev", "instance": "node", "upstream": "http://127.0.0.1:9090"}
			},
			wantCode: http.StatusConflict, wantBody: "proxy host already in use",
		},
		{
			name: "update renames onto another route's host",
			body: func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any {
				body := proxyRouteEditBody(f.b, f.b.Upstream)
				body["host"], body["updated_at"] = "app.example.test", f.b.UpdatedAt
				return body
			},
			wantCode: http.StatusConflict, wantBody: "proxy host already in use",
		},
		{
			name: "update after the route was deleted",
			body: func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any {
				response := credentialHTTPRequest(t, f.mux, f.token, http.MethodDelete, "/v1/admin/proxy-routes?id="+f.a.ID, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("delete HTTP %d: %s", response.Code, response.Body.String())
				}
				return proxyRouteEditBody(f.a, "http://127.0.0.1:9090")
			},
			wantCode: http.StatusNotFound, wantBody: "proxy route not found",
		},
		{
			name: "stale updated_at",
			body: func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any {
				f.post(t, proxyRouteEditBody(f.a, "http://127.0.0.1:7070"), http.StatusOK)
				body := proxyRouteEditBody(f.a, "http://127.0.0.1:9090")
				body["updated_at"] = f.a.UpdatedAt
				return body
			},
			wantCode: http.StatusConflict, wantBody: "proxy route changed since it was read",
		},
		{
			name: "malformed updated_at",
			body: func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any {
				body := proxyRouteEditBody(f.a, "http://127.0.0.1:9090")
				body["updated_at"] = "yesterday"
				return body
			},
			wantCode: http.StatusBadRequest, wantBody: "invalid proxy route updated_at",
		},
		{
			name: "current updated_at",
			body: func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any {
				body := proxyRouteEditBody(f.a, "http://127.0.0.1:9090")
				body["updated_at"] = f.a.UpdatedAt
				return body
			},
			wantCode: http.StatusOK,
		},
		{
			name: "client without updated_at still overwrites",
			body: func(t *testing.T, f *proxyRouteHTTPFixture) map[string]any {
				f.post(t, proxyRouteEditBody(f.a, "http://127.0.0.1:7070"), http.StatusOK)
				return proxyRouteEditBody(f.a, "http://127.0.0.1:9090")
			},
			wantCode: http.StatusOK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newProxyRouteHTTPFixture(t)
			body := tc.body(t, f)
			before := f.list(t)
			response := credentialHTTPRequest(t, f.mux, f.token, http.MethodPost, "/v1/admin/proxy-routes", body)
			if response.Code != tc.wantCode {
				t.Fatalf("HTTP %d, want %d: %s", response.Code, tc.wantCode, response.Body.String())
			}
			if tc.wantCode != http.StatusOK {
				if got := strings.TrimSpace(response.Body.String()); got != tc.wantBody {
					t.Fatalf("body = %q, want %q", got, tc.wantBody)
				}
				if after := f.list(t); after != before {
					t.Fatalf("rejected write changed routes:\nbefore %s\nafter  %s", before, after)
				}
				return
			}
			var saved proxyRouteWire
			if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
				t.Fatalf("decode saved route: %v", err)
			}
			if saved.ID != f.a.ID || saved.Upstream != "http://127.0.0.1:9090/" || saved.UpdatedAt == f.a.UpdatedAt {
				t.Fatalf("saved = %#v, seeded %#v", saved, f.a)
			}
			// The returned updated_at is the next version: sending it back must succeed once.
			next := proxyRouteEditBody(saved, "http://127.0.0.1:9191")
			next["updated_at"] = saved.UpdatedAt
			f.post(t, next, http.StatusOK)
			f.post(t, next, http.StatusConflict)
		})
	}
}
