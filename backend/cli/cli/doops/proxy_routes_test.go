package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func boolPtr(value bool) *bool { return &value }

func TestMergeProxyRouteKeepsUnchangedFields(t *testing.T) {
	existing := gatewayProxyRoute{
		ID: "route_1", Host: "app.example.com", Cluster: "c1", Instance: "i1",
		Upstream: "http://10.0.0.1:8080", Enabled: boolPtr(true),
	}
	cases := []struct {
		name   string
		change proxyRouteChange
		want   gatewayProxyRoute
	}{
		{
			name:   "empty change keeps everything",
			change: proxyRouteChange{},
			want:   existing,
		},
		{
			name:   "only upstream changes",
			change: proxyRouteChange{Upstream: " http://10.0.0.2:9090 "},
			want: gatewayProxyRoute{ID: "route_1", Host: "app.example.com", Cluster: "c1", Instance: "i1",
				Upstream: "http://10.0.0.2:9090", Enabled: boolPtr(true)},
		},
		{
			name:   "disable keeps routing",
			change: proxyRouteChange{Enabled: boolPtr(false)},
			want: gatewayProxyRoute{ID: "route_1", Host: "app.example.com", Cluster: "c1", Instance: "i1",
				Upstream: "http://10.0.0.1:8080", Enabled: boolPtr(false)},
		},
		{
			name:   "move to another target",
			change: proxyRouteChange{Cluster: "c2", Instance: "i2"},
			want: gatewayProxyRoute{ID: "route_1", Host: "app.example.com", Cluster: "c2", Instance: "i2",
				Upstream: "http://10.0.0.1:8080", Enabled: boolPtr(true)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeProxyRoute(existing, tc.change); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("merge = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestGatewayProxyRouteRequests(t *testing.T) {
	type call struct {
		method, uri string
		body        map[string]any
	}
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer admin-token" {
			t.Fatalf("authorization = %q", got)
		}
		c := call{method: r.Method, uri: r.URL.RequestURI()}
		if r.Body != nil && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&c.body)
		}
		calls = append(calls, c)
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"routes": []map[string]any{{
				"id": "route_1", "host": "app.example.com", "cluster": "c1", "instance": "i1",
				"upstream": "http://10.0.0.1:8080", "enabled": true,
			}}})
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode(c.body)
		case http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "route_1", "deleted": true})
		}
	}))
	defer server.Close()

	routes, err := gatewayProxyRoutesList(server.URL, "admin-token")
	if err != nil || len(routes) != 1 || routes[0].ID != "route_1" || routes[0].Enabled == nil || !*routes[0].Enabled {
		t.Fatalf("list = %#v err=%v", routes, err)
	}

	edited := mergeProxyRoute(routes[0], proxyRouteChange{Upstream: "http://10.0.0.2:9090"})
	saved, err := gatewayProxyRouteUpsert(server.URL, "admin-token", edited)
	if err != nil || saved.Upstream != "http://10.0.0.2:9090" || saved.Host != "app.example.com" {
		t.Fatalf("upsert = %#v err=%v", saved, err)
	}
	if err := gatewayProxyRouteDelete(server.URL, "admin-token", "route_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	wantPost := map[string]any{
		"id": "route_1", "host": "app.example.com", "cluster": "c1", "instance": "i1",
		"upstream": "http://10.0.0.2:9090", "enabled": true,
	}
	want := []call{
		{method: "GET", uri: "/v1/admin/proxy-routes"},
		{method: "POST", uri: "/v1/admin/proxy-routes", body: wantPost},
		{method: "DELETE", uri: "/v1/admin/proxy-routes?id=route_1"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v\nwant  %#v", calls, want)
	}
}
