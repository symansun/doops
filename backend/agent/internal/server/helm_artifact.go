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
	if !strings.EqualFold(repository.Hostname(), endpoint.Hostname()) ||
		managedHelmPort(repository) != managedHelmPort(endpoint) {
		return nil, "", errors.New("helm_chart_scope_mismatch")
	}
	if (username == "") != (password == "") {
		return nil, "", errors.New("managed_helm_auth_incomplete")
	}
	if _, err := resolveCredentialHost(ctx, endpoint.Hostname()); err != nil {
		return nil, "", errors.New("helm_repository_unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", errors.New("helm_chart_invalid")
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
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, "", errors.New("helm_repository_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("helm_artifact_http_%d", response.StatusCode)
	}
	if response.ContentLength > managedHelmCompressedLimit {
		return nil, "", errors.New("helm_chart_too_large")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, managedHelmCompressedLimit+1))
	if err != nil {
		return nil, "", errors.New("helm_chart_invalid")
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
	var chartYAML, valuesYAML []byte
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
		if name == root+"/Chart.yaml" || name == root+"/values.yaml" {
			if header.Typeflag != tar.TypeReg || header.Size > managedHelmYAMLLimit {
				return invalid
			}
			content, err := io.ReadAll(archive)
			if err != nil {
				return invalid
			}
			if name == root+"/Chart.yaml" {
				chartYAML = content
			} else {
				valuesYAML = content
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
	if compressed.Len() != 0 || chartYAML == nil || valuesYAML == nil {
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
	var values map[string]any
	if decodeManagedHelmYAML(valuesYAML, &values) != nil {
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
