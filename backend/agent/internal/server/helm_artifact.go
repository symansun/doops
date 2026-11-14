package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	managedHelmCompressedLimit = 32 << 20
	managedHelmExpandedLimit   = 128 << 20
	managedHelmYAMLLimit       = 1 << 20
)

var managedHelmHostnamePattern = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func fetchManagedHelmChart(ctx context.Context, repositoryURL, username, password, chart string) ([]byte, string, error) {
	if strings.HasPrefix(strings.ToLower(chart), "oci://") {
		return nil, "", errors.New("managed_helm_oci_unsupported")
	}
	repository, err := managedHelmURL(repositoryURL)
	if err != nil {
		return nil, "", errors.New("helm_repository_invalid")
	}
	endpoint, err := managedHelmURL(chart)
	if err != nil || !strings.HasSuffix(endpoint.Path, ".tgz") {
		return nil, "", errors.New("helm_chart_invalid")
	}
	// A credential is only sent to its own origin. A Chart from another origin
	// is fetched anonymously, so public Charts are never blocked by an unrelated
	// private repository binding and private credentials never reach that host.
	if !strings.EqualFold(repository.Hostname(), endpoint.Hostname()) ||
		managedHelmPort(repository) != managedHelmPort(endpoint) {
		username, password = "", ""
	}
	if (username == "") != (password == "") {
		return nil, "", errors.New("managed_helm_auth_incomplete")
	}
	response, client, err := fetchManagedHelmResponse(ctx, endpoint, username, password)
	if err != nil {
		return nil, "", err
	}
	defer client.CloseIdleConnections()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("helm_artifact_http_%d", response.StatusCode)
	}
	if response.ContentLength > managedHelmCompressedLimit {
		return nil, "", errors.New("helm_chart_too_large")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, managedHelmCompressedLimit+1))
	if err != nil {
		var requestErr *url.Error
		if errors.As(err, &requestErr) {
			err = requestErr.Err
		}
		return nil, "", fmt.Errorf("helm_repository_unavailable: %s:%s: reading chart response: %s",
			endpoint.Hostname(), managedHelmPort(endpoint), sanitizeSecretText(err.Error()))
	}
	if len(body) > managedHelmCompressedLimit {
		return nil, "", errors.New("helm_chart_too_large")
	}
	if err := validateManagedHelmChart(ctx, body); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return body, fmt.Sprintf("sha256:%x", sum), nil
}

func fetchManagedHelmResponse(ctx context.Context, endpoint *url.URL, username, password string) (*http.Response, *http.Client, error) {
	for redirects := 0; ; redirects++ {
		response, client, err := requestManagedHelmArtifact(ctx, endpoint, username, password)
		if err != nil {
			return nil, nil, err
		}
		switch response.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			// Authenticated repository responses never redirect credentials.
			if username != "" {
				return response, client, nil
			}
		default:
			return response, client, nil
		}
		next, err := response.Location()
		response.Body.Close()
		client.CloseIdleConnections()
		if redirects >= 9 {
			return nil, nil, errors.New("helm_chart_redirect_limit")
		}
		// CDN redirects may carry a server-issued signed query. Never echo it
		// in errors, and validate/pin the new HTTPS destination on every hop.
		if err != nil || next.Scheme != "https" || next.User != nil || next.Fragment != "" || next.Opaque != "" {
			return nil, nil, errors.New("helm_chart_redirect_invalid")
		}
		if _, err := managedHelmURL(next.Scheme + "://" + next.Host); err != nil {
			return nil, nil, errors.New("helm_chart_redirect_invalid")
		}
		endpoint = next
	}
}

func requestManagedHelmArtifact(ctx context.Context, endpoint *url.URL, username, password string) (*http.Response, *http.Client, error) {
	if _, err := resolveCredentialHost(ctx, endpoint.Hostname()); err != nil {
		return nil, nil, errors.New("helm_repository_unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, errors.New("helm_chart_invalid")
	}
	if username != "" && password != "" {
		request.SetBasicAuth(username, password)
	}
	request.Header.Set("Accept-Encoding", "identity")
	client := credentialHTTPClient(endpoint)
	transport := client.Transport.(*http.Transport)
	// DialContext pins the authorized host; environment proxies must not change its destination.
	transport.Proxy = nil
	transport.DisableCompression = true
	response, err := client.Do(request)
	if err != nil {
		client.CloseIdleConnections()
		var requestErr *url.Error
		if errors.As(err, &requestErr) {
			// The wrapper includes the full URL, including a CDN's signed query.
			err = requestErr.Err
		}
		detail := err.Error()
		// Protocol errors may quote response bytes that echo the request's auth.
		for _, secret := range []string{username, password, strings.TrimPrefix(request.Header.Get("Authorization"), "Basic ")} {
			if secret != "" {
				quoted := strconv.Quote(secret)
				detail = strings.ReplaceAll(detail, quoted[1:len(quoted)-1], "...REDACTED")
				detail = strings.ReplaceAll(detail, secret, "...REDACTED")
			}
		}
		return nil, nil, fmt.Errorf("helm_repository_unavailable: %s:%s: %s",
			endpoint.Hostname(), managedHelmPort(endpoint), sanitizeSecretText(detail))
	}
	return response, client, nil
}

func managedHelmURL(raw string) (*url.URL, error) {
	parsed, err := credentialOutboundURL(raw)
	if err != nil || parsed.Opaque != "" || strings.ContainsAny(raw, "\\%?#") {
		return nil, errors.New("helm_url_invalid")
	}
	for _, c := range raw {
		if c <= ' ' || c >= 127 {
			return nil, errors.New("helm_url_invalid")
		}
	}
	host := parsed.Hostname()
	if net.ParseIP(host) == nil && (len(host) > 253 || !managedHelmHostnamePattern.MatchString(host)) {
		return nil, errors.New("helm_url_invalid")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return nil, errors.New("helm_url_invalid")
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != parsed.Port() {
			return nil, errors.New("helm_url_invalid")
		}
	}
	p := strings.TrimSuffix(parsed.Path, "/")
	if p != "" && (path.Clean(p) != p || strings.Contains(p, "//")) {
		return nil, errors.New("helm_url_invalid")
	}
	return parsed, nil
}

func managedHelmPort(endpoint *url.URL) string {
	if endpoint.Port() == "" {
		return "443"
	}
	return endpoint.Port()
}

func validateManagedHelmChart(ctx context.Context, body []byte) error {
	invalid := errors.New("helm_chart_invalid")
	compressed := bytes.NewReader(body)
	stream, err := gzip.NewReader(compressed)
	if err != nil {
		return invalid
	}
	defer stream.Close()
	stream.Multistream(false)
	expanded := &io.LimitedReader{R: stream, N: managedHelmExpandedLimit + 1}
	archive := tar.NewReader(expanded)
	seen := make(map[string]bool)
	var root string
	var chartYAML []byte
	for {
		if err := ctx.Err(); err != nil {
			return errors.New("helm_repository_unavailable")
		}
		header, err := archive.Next()
		if expanded.N <= 0 {
			return errors.New("helm_chart_too_large")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return invalid
		}
		name := header.Name
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name ||
			name == ".." || strings.HasPrefix(name, "../") ||
			strings.ContainsAny(name, "\\:\x00\r\n") || seen[name] ||
			(header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir) ||
			header.Linkname != "" {
			return invalid
		}
		for key := range header.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse.") || key == "SCHILY.filetype" {
				return invalid
			}
		}
		entryRoot, _, nested := strings.Cut(name, "/")
		if root == "" {
			root = entryRoot
		}
		if entryRoot != root || (!nested && header.Typeflag != tar.TypeDir) ||
			(header.Typeflag == tar.TypeDir && header.Size != 0) {
			return invalid
		}
		seen[name] = true
		if header.Size < 0 {
			return invalid
		}
		if header.Size > managedHelmExpandedLimit || header.Size >= expanded.N {
			return errors.New("helm_chart_too_large")
		}
		if name == root+"/Chart.yaml" {
			if header.Typeflag != tar.TypeReg || header.Size > managedHelmYAMLLimit {
				return invalid
			}
			chartYAML, err = io.ReadAll(archive)
			if err != nil {
				return invalid
			}
		}
	}
	// Read through the gzip checksum and reject hidden archives after the tar end marker.
	var padding [8192]byte
	for {
		n, err := expanded.Read(padding[:])
		if expanded.N <= 0 {
			return errors.New("helm_chart_too_large")
		}
		for _, b := range padding[:n] {
			if b != 0 {
				return invalid
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil || ctx.Err() != nil {
			return invalid
		}
	}
	if compressed.Len() != 0 || chartYAML == nil {
		return invalid
	}
	var metadata struct {
		APIVersion string `yaml:"apiVersion"`
		Name       string `yaml:"name"`
		Version    string `yaml:"version"`
	}
	if decodeManagedHelmYAML(chartYAML, &metadata) != nil ||
		(metadata.APIVersion != "v1" && metadata.APIVersion != "v2") ||
		metadata.Name != root || strings.TrimSpace(metadata.Version) == "" {
		return invalid
	}
	return nil
}

func decodeManagedHelmYAML(body []byte, value any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(value); err != nil && err != io.EOF {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("helm_chart_invalid")
	}
	return nil
}
