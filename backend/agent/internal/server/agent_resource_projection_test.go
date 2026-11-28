package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestResourceProjectionAcrossEngines(t *testing.T) {
	for _, engine := range []string{"SQLite", "MySQL"} {
		t.Run(engine, func(t *testing.T) {
			var store *GatewayStore
			if engine == "MySQL" {
				store = openGatewayMySQLResourceTestStore(t)
			} else {
				t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
				var err error
				store, err = OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
				if err != nil {
					t.Fatal(err)
				}
				for _, migration := range []string{"machine_inventory", "agent_resource_sync", "cluster_inventory", "machine_diagnostics", "business_inventory", "asset_catalog", "agent_asset_scope"} {
					ddl, err := os.ReadFile("../../../docs/migrations/" + migration + ".sql")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.db.Exec(string(ddl)); err != nil {
						t.Fatal(err)
					}
				}
			}
			defer store.Close()
			t.Run("ledger", func(t *testing.T) { checkResourceProjection(t, store) })
			t.Run("credential_names", func(t *testing.T) { checkProjectionCredentialNames(t, store) })
		})
	}
}

func checkResourceProjection(t *testing.T, store *GatewayStore) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	user, token := createCredentialHTTPUser(t, store, "projection-operator", true)
	mux := http.NewServeMux()
	NewGatewayHub(store, GatewayHubOptions{}).RegisterRoutes(mux)
	fingerprint := strings.Repeat("a", 64)
	exec(`INSERT INTO inventory_machines(asset_id,name,kind,address,cluster,instance) VALUES ('native','native host','vm','','ops','left'),('assigned','assigned VM','vm','','ops','right')`)
	exec(`INSERT INTO machine_diagnostic_bindings(asset_id,cluster,instance,host_sha256) VALUES ('native','ops','left',?)`, fingerprint)
	exec(`INSERT INTO inventory_agent_assets(cluster,instance,kind,asset_id) VALUES ('ops','left','machine','assigned')`)
	exec(`INSERT INTO inventory_business_systems(asset_id,name,owner,active,revision) VALUES ('orders','Orders','ops',1,1),('other','Other','ops',1,1)`)
	for _, ns := range []string{"a", "b"} {
		exec(`INSERT INTO inventory_business_deployments(asset_id,cluster,instance,namespace,application,database_name,verified_at,verified_by) VALUES ('orders','ops','left',?,'web','','now','operator')`, ns)
	}
	exec(`INSERT INTO inventory_business_deployments(asset_id,cluster,instance,namespace,application,database_name,verified_at,verified_by) VALUES ('other','ops','right','a','web','','now','operator')`)
	exec(`INSERT INTO cluster_inventory_sources(cluster,instance,asset_id,name,kube_system_uid,verified_at,verified_by,scope) VALUES ('ops','left','k8s','Kubernetes','uid-a','now','operator','list_namespaces')`)
	const canary = "RES01-CANARY-PRIVATE"
	catalog := func(instance, id, kind, details string) {
		exec(`INSERT INTO inventory_resource_catalog(id,kind,cluster,instance,created_by,name,environment,details,identity_key,revision) VALUES (?,?,'ops',?,?,?,'test',?,?,1)`, id, kind, instance, user.ID, "Label "+id, details, id)
	}
	catalog("left", "web-id", "website", `{"scheme":"https","domain":"Portal.Example.Org","path":"/","identity":"RES01-CANARY-PRIVATE"}`)
	catalog("left", "db-id", "database", `{"engine":"PostgreSQL","host":"pg.team.svc.cluster.local","port":"5432","database":"orders","extra":"RES01-CANARY-PRIVATE"}`)
	catalog("left", "db-wrong-port", "database", `{"engine":"PostgreSQL","host":"pg.team.svc","port":"5433","database":"orders"}`)
	catalog("left", "new-site", "website", `{"scheme":"https","domain":"different.example.org","path":"/"}`)
	catalog("right", "other-site", "website", `{"scheme":"http","domain":"portal.example.org","path":"/"}`)
	exec(`INSERT INTO credentials(id,name,scope,owner_id,type,state,created_by,created_at,updated_at,revoked_at) VALUES ('opaque-private-id','ops-db','personal',?,'opaque','active',?,'now','now','')`, user.ID, user.ID)
	grant := func(id, instance, use, namespace, state string) {
		scope, _ := json.Marshal(map[string]string{"cluster": "ops", "instance": instance, "use": use, "namespace": namespace, "note": canary})
		exec(`INSERT INTO credential_grants(id,credential_id,grantee_id,scope_json,state,created_by,created_at,revoked_at) VALUES (?,'opaque-private-id',?,?,?,?, 'now','')`, id, user.ID, string(scope), state, user.ID)
	}
	exec(`INSERT INTO credential_versions(id,credential_id,payload_ciphertext,payload_digest,state,created_by,created_at,promoted_at,revoked_at) VALUES ('v1','opaque-private-id',?,'digest','active',?,'now','now','')`, canary, user.ID)
	grant("z", "left", "opaqueSecret", "b", "active")
	grant("a", "left", "database", "a", "active")
	grant("c", "left", "database", "c", "active")
	grant("d", "right", "opaqueSecret", "d", "active")
	grant("r", "right", "database", "a", "revoked")

	ctx := context.Background()
	agent := &GatewayAgent{Cluster: "ops", Instance: "left", Generation: 1, ConnectedAt: time.Now().UTC()}
	available := true
	for _, instance := range []string{"left", "right"} {
		if err := store.MarkAgentOnline(AgentStatus{Cluster: "ops", Instance: instance, Generation: 1, ConnectedAt: agent.ConnectedAt, Mode: "linux", KubernetesAvailable: &available}); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(snapshot agentResourceSnapshot) targetManifest {
		t.Helper()
		snapshot.canonicalize()
		m, err := store.reconcileAgentResources(ctx, agent, &snapshot, agentResourceDigest(snapshot))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(m)
		for _, private := range []string{canary, "opaque-private-id", "second-private-id", "pg.team.svc", "other-site"} {
			if strings.Contains(string(b), private) {
				t.Errorf("unexpected %q in target manifest", private)
			}
		}
		return m.Manifest
	}
	byRef := func(m targetManifest) map[string]targetManifestResource {
		t.Helper()
		out := map[string]targetManifestResource{}
		for _, r := range m.Resources {
			if _, dup := out[r.Ref]; dup {
				t.Errorf("duplicate reference %s", r.Ref)
			}
			if !validManifestRef(r.Ref) {
				t.Errorf("unusable manifest reference %s", r.Ref)
			}
			out[r.Ref] = r
		}
		return out
	}
	checkState := func(rows map[string]targetManifestResource, ref, want string) {
		t.Helper()
		if got := rows[ref].State; got != want {
			t.Errorf("%s = %q, want %q", ref, got, want)
		}
	}
	snapshot := agentResourceSnapshot{Mode: "linux", KubernetesAvailable: true, Complete: []string{"machine", "cluster", "database"}, Resources: []agentResourceObservation{
		{Kind: "machine", Key: "host", Name: "host", Identity: fingerprint},
		{Kind: "cluster", Key: "current", Name: "current", Identity: "uid-a"},
		{Kind: "database", Key: "team/pg/postgres", Name: "pg", Identity: databaseObservationIdentity("postgres", "pg.team.svc", "5432", "orders")},
		{Kind: "database", Key: "team/candidate/mysql", Name: "candidate"},
	}}
	// DoWeb's site-list supplies a site ID and domain, independently of a catalog ID.
	dir := t.TempDir()
	script := filepath.Join(dir, "site-list")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat <<'JSON'\n{\"sites\":[{\"site_id\":\"portal-release\",\"domain\":\"portal.example.org\",\"extra\":\"RES01-CANARY-PRIVATE\"},{\"site_id\":\"new-site\",\"domain\":\"new.example.org\"}]}\nJSON\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOWEB_NODE", "/bin/sh")
	t.Setenv("DOWEB_SCRIPT", script)
	sites, _ := probeAgentSites(ctx)
	if len(sites) != 2 {
		t.Fatalf("site-list observations = %v", sites)
	}
	snapshot.Resources = append(snapshot.Resources, sites...)
	first := publish(snapshot)
	rows := byRef(first)
	for ref, want := range map[string]string{"machine/native": "verified", "machine/assigned": "unverified", "system/orders": "unverified", "system/other": "", "credential/ops-db": "unverified", "site/web-id": "verified", "database/db-id": "verified", "database/db-wrong-port": "unverified", "cluster/k8s": "verified"} {
		checkState(rows, ref, want)
	}
	checkState(rows, "site/new-site", "unverified")
	if got := rows["credential/ops-db"].Uses; !slices.Equal(got, []string{"database", "opaqueSecret"}) {
		t.Errorf("uses = %q", got)
	}
	if first.Counts["site"] != 3 || first.Counts["database"] != 3 {
		t.Errorf("unexpected counts after association: %v", first.Counts)
	}
	for _, r := range rows {
		if r.State == "discovered" && (r.Kind == "site" || r.Kind == "database") && (r.CanRegister || r.Reason == "") {
			t.Errorf("missing manual registration boundary: %+v", r)
		}
	}
	// A metadata-only observation cannot create a URL or a database connection.
	for _, r := range rows {
		if r.State != "discovered" || (r.Kind != "site" && r.Kind != "database") {
			continue
		}
		reply := credentialHTTPRequest(t, mux, token, "POST", "/v1/assets/target-manifest", map[string]string{"action": "register", "cluster": "ops", "instance": "left", "ref": r.Ref})
		if reply.Code != 409 || !strings.Contains(reply.Body.String(), "resource_requires_inventory_fields") || !strings.Contains(reply.Body.String(), r.Reason) {
			t.Errorf("register %s: %d %s", r.Ref, reply.Code, reply.Body.String())
		}
	}
	if next := publish(snapshot); next.Revision != first.Revision {
		t.Errorf("identical report advanced revision %d to %d", first.Revision, next.Revision)
	}
	right, err := store.readTargetManifest(ctx, "ops", "right")
	if err != nil {
		t.Fatal(err)
	}
	other := byRef(right.Manifest)
	checkState(other, "system/orders", "")
	checkState(other, "system/other", "unverified")
	checkState(other, "site/other-site", "unverified")
	if uses := other["credential/ops-db"].Uses; !slices.Equal(uses, []string{"opaqueSecret"}) {
		t.Errorf("right target uses = %v", uses)
	}
	// Prove the advertised manual workflow through the same published catalog API operators use.
	for _, entry := range []struct {
		kind, refKind string
		details       map[string]string
		state         string
	}{
		{"website", "site", map[string]string{"scheme": "https", "domain": "new.example.org", "path": "/"}, "verified"},
		{"database", "database", map[string]string{"engine": "MySQL", "host": "candidate.team.svc", "port": "3306", "database": "orders"}, "unverified"},
	} {
		request := map[string]any{"op": "create", "kind": entry.kind, "cluster": "ops", "instance": "left", "name": "Manual entry", "details": map[string]string{}}
		reply := credentialHTTPRequest(t, mux, token, "POST", "/v1/assets/catalog", request)
		if reply.Code != 400 {
			t.Fatalf("incomplete %s catalog request: %d %s", entry.kind, reply.Code, reply.Body.String())
		}
		request["details"] = entry.details
		reply = credentialHTTPRequest(t, mux, token, "POST", "/v1/assets/catalog", request)
		var record struct {
			ID string `json:"id"`
		}
		if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &record) != nil || record.ID == "" {
			t.Fatalf("manual %s registration: %d %s", entry.kind, reply.Code, reply.Body.String())
		}
		checkState(byRef(publish(snapshot)), entry.refKind+"/"+record.ID, entry.state)
	}
	// Distinct credentials may share a legal name across owners. Never combine their permissions.
	otherOwner, err := store.CreateUser("projection-owner")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO credentials(id,name,scope,owner_id,type,state,created_by,created_at,updated_at,revoked_at) VALUES ('second-private-id','ops-db','personal',?,'opaque','active',?,'now','now','')`, otherOwner.ID, otherOwner.ID)
	exec(`INSERT INTO credential_grants(id,credential_id,grantee_id,scope_json,state,created_by,created_at,revoked_at) VALUES ('collision','second-private-id',?,'{"cluster":"ops","instance":"left","use":"database"}','active',?,'now','')`, user.ID, otherOwner.ID)
	collision := byRef(publish(snapshot))["credential/ops-db"]
	if collision.State != "conflict" || collision.Reason == "" || len(collision.Uses) != 0 || collision.CredentialType != "" {
		t.Fatalf("ambiguous credential = %+v", collision)
	}
	exec(`UPDATE credential_grants SET state='revoked' WHERE id='collision'`)
	if uses := byRef(publish(snapshot))["credential/ops-db"].Uses; !slices.Equal(uses, []string{"database", "opaqueSecret"}) {
		t.Errorf("after revoke: uses = %q", uses)
	}
	// Same DoWeb domain with two catalog routes cannot distinguish their registrations.
	catalog("left", "web-path", "website", `{"scheme":"https","domain":"portal.example.org","path":"/app"}`)
	ambiguous := byRef(publish(snapshot))
	checkState(ambiguous, "site/web-id", "unverified")
	checkState(ambiguous, "site/web-path", "unverified")
	// An unreachable or unauthenticated API cannot advance a previously verified UID's misses.
	stubKubernetesProbe(t, false)
	unavailable := collectAgentResourceSnapshot(ctx)
	for range 3 {
		checkState(byRef(publish(unavailable)), "cluster/k8s", "verified")
	}
	var misses int
	if err := store.db.QueryRow(`SELECT misses FROM agent_observations WHERE cluster='ops' AND instance='left' AND kind='cluster'`).Scan(&misses); err != nil || misses != 0 {
		t.Fatalf("cluster misses after failed probes = %d, %v", misses, err)
	}
	// A working API with a different UID conflicts; only complete empty lists count towards loss.
	for i := range snapshot.Resources {
		if snapshot.Resources[i].Kind == "cluster" {
			snapshot.Resources[i].Identity = "uid-b"
		}
	}
	checkState(byRef(publish(snapshot)), "cluster/k8s", "conflict")
	missing := agentResourceSnapshot{Mode: "linux", KubernetesAvailable: true, Complete: []string{"database"}}
	for i := 1; i <= 3; i++ {
		want := "verified"
		if i == 3 {
			want = "lost"
		}
		checkState(byRef(publish(missing)), "database/db-id", want)
	}
	checkState(byRef(publish(snapshot)), "database/db-id", "verified")
	// A stored observation must satisfy the same metadata boundary as a fresh report.
	exec(`UPDATE agent_observations SET details=? WHERE cluster='ops' AND instance='left' AND kind='machine'`, `{"kind":"machine","key":"host","name":"token:RES01-CANARY-PRIVATE"}`)
	if _, err := store.readTargetManifest(ctx, "ops", "left"); err == nil || strings.Contains(err.Error(), canary) {
		t.Errorf("unsafe persisted observation: expected a metadata error, got %v", err)
	}
}

func TestResourceProjectionUnavailableCluster(t *testing.T) {
	stubKubernetesProbe(t, false)
	t.Setenv("PATH", t.TempDir())
	snapshot := collectAgentResourceSnapshot(context.Background())
	if !snapshot.valid() || slices.Contains(snapshot.Complete, "cluster") {
		t.Fatalf("unreachable API reported as a complete cluster observation: %v", snapshot.Complete)
	}
}

func TestResourceProjectionDatabaseProbeMetadata(t *testing.T) {
	dir := t.TempDir()
	objects := strings.ReplaceAll(databaseInventorySample, `"image":"postgres:16"`, `"image":"postgres:16","env":[{"name":"POSTGRES_DB","value":"orders"},{"name":"PRIVATE_SETTING","value":"RES01-PROBE-CANARY"}]`)
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte("#!/bin/sh\ncat <<'JSON'\n{\"items\":"+objects+"}\nJSON\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	rows, complete := probeAgentDatabases(context.Background())
	if !complete || len(rows) != 1 || rows[0].Key != "qa/orders-postgres/postgres" || rows[0].Identity != databaseObservationIdentity("PostgreSQL", "orders.qa.svc.cluster.local", "15432", "orders") || rows[0].Identity == "" {
		t.Fatalf("database observations = %+v, complete = %v", rows, complete)
	}
	wire, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(wire), "PROBE-CANARY") || strings.Contains(string(wire), "orders.qa.svc") {
		t.Fatalf("probe metadata included private material, err = %v", err)
	}
}

func checkProjectionCredentialNames(t *testing.T, store *GatewayStore) {
	t.Helper()
	owner, token := createCredentialHTTPUser(t, store, "name-owner", true)
	other, err := store.CreateUser("name-other")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewGatewayHub(store, GatewayHubOptions{}).RegisterRoutes(mux)
	available := false
	for _, inst := range []string{"primary", "other"} {
		if err := store.MarkAgentOnline(AgentStatus{Cluster: "names", Instance: inst, Generation: 1, ConnectedAt: time.Now().UTC(), Mode: "linux", KubernetesAvailable: &available}); err != nil {
			t.Fatal(err)
		}
	}
	grantName := func(credentialID, actor, instance string) string {
		t.Helper()
		scope, _ := json.Marshal(map[string]string{"cluster": "names", "instance": instance, "use": "opaqueSecret"})
		id := randomHex(12)
		if _, err := store.db.Exec(`INSERT INTO credential_grants(id,credential_id,grantee_id,scope_json,state,created_by,created_at,revoked_at) VALUES (?,?,?,?,'active',?,'now','')`, id, credentialID, actor, string(scope), actor); err != nil {
			t.Fatal(err)
		}
		return id
	}
	create := func(name, actor string) string {
		t.Helper()
		credential, err := store.CreateCredential(CredentialCreateRequest{Name: name, CreatedBy: actor, OwnerID: actor, Scope: CredentialScopePersonal, Type: CredentialTypeOpaque})
		if err != nil {
			t.Fatalf("creating test credential: %v", err)
		}
		return credential.ID
	}
	read := func(instance string) map[string]targetManifestResource {
		t.Helper()
		reply := credentialHTTPRequest(t, mux, token, "GET", "/v1/assets/target-manifest?cluster=names&instance="+instance, nil)
		var status targetManifestStatus
		if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &status) != nil {
			t.Fatalf("GET manifest for %s: %d %s", instance, reply.Code, reply.Body.String())
		}
		resources := map[string]targetManifestResource{}
		for _, r := range status.Manifest.Resources {
			if _, exists := resources[r.Ref]; exists {
				t.Fatalf("duplicate ref %s", r.Ref)
			}
			resources[r.Ref] = r
		}
		return resources
	}
	names := []string{"ops db", "研发 数据库", "ops/db", "ops%2Fdb", "ops%20db", ".", "..", "night+ops@dc/100%~", "🗝️ (read-only)", strings.Repeat("库", 128), "Ops DB"}
	for _, name := range names {
		actor := owner.ID
		if name == "Ops DB" {
			actor = other.ID
		}
		grantName(create(name, actor), actor, "primary")
	}
	shared := create("研发 数据库", other.ID)
	grantName(shared, other.ID, "other")
	primary := read("primary")
	if len(primary) != len(names) {
		t.Errorf("projected %d credentials, expected %d", len(primary), len(names))
	}
	for _, name := range names {
		segment := url.PathEscape(name)
		if name == "." || name == ".." {
			segment = strings.Repeat("%2E", len(name))
		}
		ref := "credential/" + segment
		if name == "night+ops@dc/100%~" {
			ref = "credential/night%2Bops%40dc%2F100%25%7E"
		}
		r := primary[ref]
		if r.Name != name || r.State != "unverified" || !slices.Equal(r.Uses, []string{"opaqueSecret"}) || !validManifestRef(ref) {
			t.Errorf("missing or unusable credential ref %q", ref)
		}
	}
	for _, invalid := range []string{"ops db", ".", "bad%", "x%00", "ops%2fdb", "%41", "token%3Dprivate"} {
		if validManifestRef("credential/" + invalid) {
			t.Errorf("accepted noncanonical or unsafe credential segment %q", invalid)
		}
	}
	duplicate := grantName(shared, other.ID, "primary")
	ref := "credential/" + url.PathEscape("研发 数据库")
	if entry := read("primary")[ref]; entry.State != "conflict" || len(entry.Uses) != 0 {
		t.Errorf("same name did not conflict: %+v", entry)
	}
	if entry := read("other")[ref]; entry.State != "unverified" || len(entry.Uses) != 1 {
		t.Errorf("target-local conflict leaked: %+v", entry)
	}
	if _, err := store.db.Exec(`UPDATE credential_grants SET state='revoked' WHERE id=?`, duplicate); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{"token=RES01-NAME-CANARY", "postgres://probe:RES01-NAME-CANARY@db.invalid/app", "reader\nRES01-NAME-CANARY", "user:RES01-NAME-CANARY@tcp(db.invalid:3306)/app"} {
		id := grantName(create(unsafe, owner.ID), owner.ID, "primary")
		reply := credentialHTTPRequest(t, mux, token, "GET", "/v1/assets/target-manifest?cluster=names&instance=primary", nil)
		if reply.Code != 503 || strings.Contains(reply.Body.String(), "RES01-NAME-CANARY") {
			t.Errorf("unsafe metadata disclosure or wrong HTTP status %d", reply.Code)
		}
		if len(read("other")) != 1 {
			t.Fatal("another target was affected by unsafe metadata")
		}
		if _, err := store.db.Exec(`UPDATE credential_grants SET state='revoked' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if len(read("primary")) != len(names) {
		t.Fatal("safe credentials did not recover after revocation")
	}
}
