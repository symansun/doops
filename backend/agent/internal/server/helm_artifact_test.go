package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type managedHelmTestEntry struct {
	header tar.Header
	body   []byte
}

func managedHelmTestEntries() []managedHelmTestEntry {
	return []managedHelmTestEntry{
		{header: tar.Header{Name: "zhiyong/Chart.yaml"}, body: []byte("apiVersion: v2\nname: zhiyong\nversion: 1.0.0\n")},
		{header: tar.Header{Name: "zhiyong/values.yaml"}, body: []byte("image:\n  repository: example.test/team/app\n")},
		{header: tar.Header{Name: "zhiyong/templates/deployment.yaml"}, body: []byte("kind: Deployment\n")},
	}
}

type managedHelmZeroReader struct{}

func (managedHelmZeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func managedHelmTestArchive(t *testing.T, entries []managedHelmTestEntry) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := entry.header
		if header.Size == 0 {
			header.Size = int64(len(entry.body))
		}
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			var body io.Reader = bytes.NewReader(entry.body)
			if entry.body == nil {
				body = managedHelmZeroReader{}
			}
			if _, err := io.CopyN(tw, body, header.Size); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func managedHelmTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	allowCredentialPrivateEndpointsForTest(t)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	previous := http.DefaultTransport
	transport := previous.(*http.Transport).Clone()
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = previous
		transport.CloseIdleConnections()
	})
	return server
}

func TestFetchManagedHelmChartDownloadsAuthorizedManagedArtifact(t *testing.T) {
	const canary = "managed-helm-canary-secret"
	const chartPath = "/charts/example/zhiyong/education/zhiyong/v/1.0.0.tgz"
	chart := managedHelmTestArchive(t, managedHelmTestEntries())
	var requests atomic.Int64
	server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		username, password, ok := r.BasicAuth()
		if r.Method != http.MethodGet || r.URL.Path != chartPath || !ok || username != "helm" || password != canary {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("artifact bytes may be transparently decompressed")
		}
		_, _ = w.Write(chart)
	}))
	repository := server.URL + "/charts/example/zhiyong/education"
	for _, repo := range []string{repository, repository + "/", server.URL + chartPath} {
		body, digest, err := fetchManagedHelmChart(context.Background(), repo, "helm", canary, server.URL+chartPath)
		if err != nil || !bytes.Equal(body, chart) || digest != fmt.Sprintf("sha256:%x", sha256.Sum256(chart)) {
			t.Fatalf("body matches=%t digest=%q error=%v", bytes.Equal(body, chart), digest, err)
		}
	}
	if requests.Load() != 3 {
		t.Fatalf("requests=%d, want one request per call", requests.Load())
	}
}

func TestFetchManagedHelmChartAllowsAnonymousHTTPSWithoutBasicAuth(t *testing.T) {
	chart := managedHelmTestArchive(t, managedHelmTestEntries())
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int64
			server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "" {
					t.Error("anonymous chart request sent Authorization")
				}
				w.Header().Set("WWW-Authenticate", `Basic realm="synthetic"`)
				w.WriteHeader(status)
				_, _ = w.Write(chart)
			}))
			repository := server.URL + "/charts/team"
			body, digest, err := fetchManagedHelmChart(context.Background(), repository, "", "", repository+"/app.tgz")
			if status == http.StatusOK {
				if err != nil || !bytes.Equal(body, chart) || digest != fmt.Sprintf("sha256:%x", sha256.Sum256(chart)) {
					t.Fatalf("body matches=%t digest=%q error=%v", bytes.Equal(body, chart), digest, err)
				}
			} else if err == nil || err.Error() != "helm_artifact_http_401" || body != nil || digest != "" {
				t.Fatalf("body nil=%t digest=%q error=%v", body == nil, digest, err)
			}
			if requests.Load() != 1 {
				t.Fatalf("anonymous requests=%d, want exactly one", requests.Load())
			}
		})
	}
}

func TestFetchManagedHelmChartRejectsPartialBasicAuthBeforeHTTP(t *testing.T) {
	var requests atomic.Int64
	server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	repository := server.URL + "/charts/team"
	for name, credentials := range map[string][2]string{
		"username only": {"helm", ""},
		"password only": {"", "synthetic-password"},
	} {
		t.Run(name, func(t *testing.T) {
			body, digest, err := fetchManagedHelmChart(context.Background(), repository, credentials[0], credentials[1], repository+"/app.tgz")
			if err == nil || err.Error() != "managed_helm_auth_incomplete" || body != nil || digest != "" {
				t.Fatalf("body nil=%t digest=%q error=%v", body == nil, digest, err)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("partial credentials reached server %d times", requests.Load())
	}
}

func TestFetchManagedHelmChartRejectsInvalidURLsAndUnauthorizedScopeBeforeHTTP(t *testing.T) {
	var requests atomic.Int64
	server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	repository := server.URL + "/charts/team"
	chart := repository + "/app/v/1.0.0.tgz"
	for _, test := range []struct {
		name, repository, chart, want string
	}{
		{"missing chart", repository, "", "helm_chart_invalid"},
		{"named chart", repository, "team/app", "helm_chart_invalid"},
		{"OCI", repository, "oci://helm.example.com/example/zhiyong/education/zhiyong", "managed_helm_oci_unsupported"},
		{"OCI repository", "oci://helm.example.com/charts/team", chart, "helm_repository_invalid"},
		{"HTTP", repository, strings.Replace(chart, "https:", "http:", 1), "helm_chart_invalid"},
		{"HTTP repository", strings.Replace(repository, "https:", "http:", 1), chart, "helm_repository_invalid"},
		{"userinfo", repository, strings.Replace(chart, "https://", "https://user:synthetic@", 1), "helm_chart_invalid"},
		{"repository userinfo", strings.Replace(repository, "https://", "https://user:synthetic@", 1), chart, "helm_repository_invalid"},
		{"query", repository, chart + "?token=synthetic", "helm_chart_invalid"},
		{"empty query", repository, chart + "?", "helm_chart_invalid"},
		{"fragment", repository, chart + "#synthetic", "helm_chart_invalid"},
		{"empty fragment", repository, chart + "#", "helm_chart_invalid"},
		{"repository query", repository + "?", chart, "helm_repository_invalid"},
		{"repository fragment", repository + "#", chart, "helm_repository_invalid"},
		{"whitespace", repository, " " + chart, "helm_chart_invalid"},
		{"host", repository, "https://example.test/charts/team/app/v/1.0.0.tgz", "helm_chart_scope_mismatch"},
		{"port", repository, "https://127.0.0.1:1/charts/team/app/v/1.0.0.tgz", "helm_chart_scope_mismatch"},
		{"traversal", repository, repository + "/../elsewhere/app.tgz", "helm_chart_invalid"},
		{"encoded traversal", repository, repository + "/%2e%2e/elsewhere/app.tgz", "helm_chart_invalid"},
		{"encoded slash", repository, repository + "/app%2fv/1.0.0.tgz", "helm_chart_invalid"},
		{"double escape", repository, repository + "/%252e%252e/app.tgz", "helm_chart_invalid"},
		{"backslash", repository, repository + "/app\\v/1.0.0.tgz", "helm_chart_invalid"},
		{"double slash", repository, repository + "//app.tgz", "helm_chart_invalid"},
		{"dot", repository, repository + "/./app.tgz", "helm_chart_invalid"},
		{"index", repository, repository + "/index.yaml", "helm_chart_invalid"},
		{"invalid port", "https://example.test:65536/charts/team", chart, "helm_repository_invalid"},
		{"empty port", "https://example.test:/charts/team", chart, "helm_repository_invalid"},
		{"invalid host", "https://bad_host.test/charts/team", chart, "helm_repository_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, digest, err := fetchManagedHelmChart(context.Background(), test.repository, "helm", "url-canary-secret", test.chart)
			if err == nil || err.Error() != test.want || body != nil || digest != "" {
				t.Fatalf("body nil=%t digest=%q error=%v, want %s", body == nil, digest, err, test.want)
			}
			assertCredentialCanaryNotInText(t, "error", err.Error(), "url-canary-secret")
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("unauthorized requests=%d", requests.Load())
	}
}

func TestValidateManagedHelmChartAcceptsEmptyValues(t *testing.T) {
	for _, values := range []string{"", " \n", "# No configurable values.\n"} {
		t.Run(fmt.Sprintf("%q", values), func(t *testing.T) {
			entries := managedHelmTestEntries()
			entries[1].body = []byte(values)
			if err := validateManagedHelmChart(context.Background(), managedHelmTestArchive(t, entries)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFetchManagedHelmChartRejectsPrivateEndpoints(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.0.1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "[::1]", "[fe80::1]", "[fc00::1]", "[::ffff:127.0.0.1]"} {
		t.Run(host, func(t *testing.T) {
			repo := "https://" + host + "/charts/team"
			body, digest, err := fetchManagedHelmChart(context.Background(), repo, "helm", "private-canary", repo+"/app.tgz")
			if err == nil || err.Error() != "helm_repository_unavailable" || body != nil || digest != "" {
				t.Fatalf("private endpoint error=%v digest=%q", err, digest)
			}
		})
	}
}

func TestFetchManagedHelmChartDoesNotRetryFailuresOrFollowRedirects(t *testing.T) {
	var destinationRequests atomic.Int64
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError, http.StatusNoContent, http.StatusPartialContent, http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int64
			server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if username, password, ok := r.BasicAuth(); !ok || username != "helm" || password != "no-retry-canary" {
					t.Error("authentication absent or changed")
				}
				w.Header().Set("Location", destination.URL+"/charts/team/app.tgz")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("no-retry-canary"))
			}))
			repo := server.URL + "/charts/team"
			body, digest, err := fetchManagedHelmChart(context.Background(), repo, "helm", "no-retry-canary", repo+"/app.tgz")
			if err == nil || err.Error() != fmt.Sprintf("helm_artifact_http_%d", status) || body != nil || digest != "" || requests.Load() != 1 {
				t.Fatalf("error=%v digest=%q requests=%d", err, digest, requests.Load())
			}
			assertCredentialCanaryNotInText(t, "response error", err.Error(), "no-retry-canary")
		})
	}
	if destinationRequests.Load() != 0 {
		t.Fatalf("redirect destination received %d requests", destinationRequests.Load())
	}
}

func TestFetchManagedHelmChartHTTPFailureDoesNotReadResponseBody(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int64
			server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("X-Canary", "response-header-canary")
				w.Header().Set("Content-Length", "1024")
				w.WriteHeader(status)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			body, digest, err := fetchManagedHelmChart(ctx, server.URL, "helm", "unread-body-canary", server.URL+"/app.tgz")
			if err == nil || err.Error() != fmt.Sprintf("helm_artifact_http_%d", status) || body != nil || digest != "" || ctx.Err() != nil || requests.Load() != 1 {
				t.Fatalf("error=%v digest=%q context=%v requests=%d", err, digest, ctx.Err(), requests.Load())
			}
		})
	}
}

func TestFetchManagedHelmChartDisablesEnvironmentProxy(t *testing.T) {
	var proxyRequests atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyRequests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	chart := managedHelmTestArchive(t, managedHelmTestEntries())
	server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(chart) }))
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	http.DefaultTransport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)
	if _, _, err := fetchManagedHelmChart(context.Background(), server.URL, "helm", "proxy-canary", server.URL+"/app.tgz"); err != nil {
		t.Fatal(err)
	}
	if proxyRequests.Load() != 0 {
		t.Fatalf("proxy received %d requests", proxyRequests.Load())
	}
}

func TestFetchManagedHelmChartRejectsNonChartsAndUnsafeArchives(t *testing.T) {
	valid := managedHelmTestArchive(t, managedHelmTestEntries())
	corrupt := bytes.Clone(valid)
	corrupt[len(corrupt)-8] ^= 1
	for _, test := range []struct {
		name string
		edit func([]managedHelmTestEntry) []managedHelmTestEntry
		body []byte
	}{
		{name: "empty", body: []byte{}},
		{name: "index YAML", body: []byte("apiVersion: v1\nentries: {}\n")},
		{name: "HTML", body: []byte("<html>login</html>")},
		{name: "truncated gzip", body: valid[:len(valid)-4]},
		{name: "bad checksum", body: corrupt},
		{name: "trailing data", body: append(bytes.Clone(valid), []byte("extra")...)},
		{name: "multiple gzip streams", body: append(bytes.Clone(valid), valid...)},
		{name: "missing Chart.yaml", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry { return e[1:] }},
		{name: "missing values.yaml", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry { return append(e[:1], e[2:]...) }},
		{name: "invalid metadata", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[0].body = []byte("apiVersion: v2\nname: zhiyong\n")
			return e
		}},
		{name: "invalid apiVersion", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[0].body = []byte("apiVersion: v3\nname: zhiyong\nversion: 1.0.0\n")
			return e
		}},
		{name: "wrong chart name", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[0].body = []byte("apiVersion: v2\nname: another\nversion: 1.0.0\n")
			return e
		}},
		{name: "bad YAML", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry { e[0].body = []byte("[broken"); return e }},
		{name: "bad values", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry { e[1].body = []byte("[broken"); return e }},
		{name: "values sequence", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry { e[1].body = []byte("- value\n"); return e }},
		{name: "duplicate YAML key", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[1].body = []byte("key: one\nkey: two\n")
			return e
		}},
		{name: "multiple YAML documents", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[1].body = []byte("key: value\n---\nother: value\n")
			return e
		}},
		{name: "YAML too large", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[1].body = bytes.Repeat([]byte("x"), managedHelmYAMLLimit+1)
			return e
		}},
		{name: "traversal", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[2].header.Name = "zhiyong/../outside"
			return e
		}},
		{name: "absolute path", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[2].header.Name = "/zhiyong/templates/app"
			return e
		}},
		{name: "backslash", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[2].header.Name = "zhiyong/templates\\..\\outside"
			return e
		}},
		{name: "multiple roots", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[2].header.Name = "other/templates/app"
			return e
		}},
		{name: "root file", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry { e[2].header.Name = "outside"; return e }},
		{name: "duplicate file", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry { return append(e, e[0]) }},
		{name: "symlink", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[2] = managedHelmTestEntry{header: tar.Header{Name: "zhiyong/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}}
			return e
		}},
		{name: "hardlink", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[2] = managedHelmTestEntry{header: tar.Header{Name: "zhiyong/link", Typeflag: tar.TypeLink, Linkname: "zhiyong/Chart.yaml"}}
			return e
		}},
		{name: "device", edit: func(e []managedHelmTestEntry) []managedHelmTestEntry {
			e[2] = managedHelmTestEntry{header: tar.Header{Name: "zhiyong/device", Typeflag: tar.TypeChar}}
			return e
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			if test.edit != nil {
				body = managedHelmTestArchive(t, test.edit(managedHelmTestEntries()))
			}
			var requests atomic.Int64
			server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write(body)
			}))
			result, digest, err := fetchManagedHelmChart(context.Background(), server.URL, "helm", "archive-canary", server.URL+"/app.tgz")
			if err == nil || err.Error() != "helm_chart_invalid" || result != nil || digest != "" || requests.Load() != 1 {
				t.Fatalf("error=%v digest=%q requests=%d", err, digest, requests.Load())
			}
		})
	}
}

func TestFetchManagedHelmChartEnforcesCompressedAndExpandedLimits(t *testing.T) {
	for _, test := range []struct {
		name    string
		chunked bool
		body    func() []byte
	}{
		{"compressed length", false, func() []byte { return bytes.Repeat([]byte("x"), managedHelmCompressedLimit+1) }},
		{"compressed stream", true, func() []byte { return bytes.Repeat([]byte("x"), managedHelmCompressedLimit+1) }},
		{"expanded file", false, func() []byte {
			return managedHelmTestArchive(t, append(managedHelmTestEntries(), managedHelmTestEntry{header: tar.Header{Name: "zhiyong/bomb", Size: managedHelmExpandedLimit + 1}}))
		}},
		{"expanded cumulative", false, func() []byte {
			return managedHelmTestArchive(t, append(managedHelmTestEntries(),
				managedHelmTestEntry{header: tar.Header{Name: "zhiyong/first", Size: managedHelmExpandedLimit / 2}},
				managedHelmTestEntry{header: tar.Header{Name: "zhiyong/second", Size: managedHelmExpandedLimit / 2}}))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := test.body()
			var requests atomic.Int64
			server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				if test.chunked {
					w.(http.Flusher).Flush()
				} else {
					w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				}
				_, _ = w.Write(body)
			}))
			result, digest, err := fetchManagedHelmChart(context.Background(), server.URL, "helm", "limit-canary", server.URL+"/app.tgz")
			if err == nil || err.Error() != "helm_chart_too_large" || result != nil || digest != "" || requests.Load() != 1 {
				t.Fatalf("error=%v digest=%q requests=%d", err, digest, requests.Load())
			}
		})
	}
}

func TestHelmCredentialBindsWithoutOldArtifactAndDownloadFailureStopsMutation(t *testing.T) {
	bin, argsLog := installCredentialTestCommands(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var requests atomic.Int64
	server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "materialize-failure-canary", http.StatusUnauthorized)
	}))
	request := CredentialMaterializeRequest{
		CredentialID: "cred_helm", GrantID: "grant_helm", VersionID: "ver_helm", CredentialType: CredentialTypeHelmRepository,
		Scope: CredentialGrantScope{
			Cluster: "doops", Instance: "runner", Namespace: "kz-ops", Use: CredentialUseHelmPull,
			Workload:    CredentialPlanWorkload{Kind: "deployment", Name: "doops-agent-live"},
			HelmRelease: "doops-agent", HelmChart: server.URL + "/charts/team/app.tgz",
		},
		Payload: mustJSON(t, map[string]string{"url": server.URL + "/charts/team", "username": "helm", "password": "materialize-failure-canary"}),
	}
	result, err := materializeCredential(context.Background(), request)
	if err != nil || result.Status != "verified" || requests.Load() != 0 {
		t.Fatalf("status=%q error=%v requests=%d", result.Status, err, requests.Load())
	}
	t.Cleanup(func() {
		_, _ = cleanupNativeHelmCredential(CredentialCleanupRequest{
			Namespace: request.Scope.Namespace, CredentialID: request.CredentialID, GrantID: request.GrantID, AnyVersion: true,
		})
	})
	_, _, err = fetchManagedHelmChart(t.Context(), server.URL, "helm", "materialize-failure-canary", server.URL+"/charts/team/new.tgz")
	if err == nil || err.Error() != "helm_artifact_http_401" || requests.Load() != 1 {
		t.Fatalf("current authenticated download failure was hidden: %v requests=%d", err, requests.Load())
	}
	if args, readErr := os.ReadFile(argsLog); readErr != nil && !os.IsNotExist(readErr) || len(args) != 0 {
		t.Fatal("artifact failure executed target commands")
	}
	assertCredentialCanaryNotInText(t, "materialize failure", err.Error(), "materialize-failure-canary")
}
