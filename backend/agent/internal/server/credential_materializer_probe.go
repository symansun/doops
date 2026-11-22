package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func verifyRegistryManifest(ctx context.Context, server, username, password, repository, reference string) (string, error) {
	repository = strings.Trim(strings.TrimSpace(repository), "/")
	reference = strings.TrimSpace(reference)
	if repository == "" || reference == "" || strings.ContainsAny(repository+reference, " \t\r\n?#") {
		return "", errors.New("registry_verification_context_invalid")
	}
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	endpoint := strings.TrimRight(server, "/") + "/v2/" + repository + "/manifests/" + reference
	request, err := registryManifestRequest(ctx, endpoint)
	if err != nil {
		return "", errors.New("registry_request_invalid")
	}
	request.SetBasicAuth(username, password)
	response, err := credentialHTTPClient(request.URL).Do(request)
	if err != nil {
		return "", errors.New("registry_manifest_unavailable")
	}
	if response.StatusCode == http.StatusUnauthorized {
		challenge := response.Header.Get("WWW-Authenticate")
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		token, err := exchangeRegistryBearerToken(
			ctx, request.URL, challenge, username, password,
		)
		if err != nil {
			return "", err
		}
		request, err = registryManifestRequest(ctx, endpoint)
		if err != nil {
			return "", errors.New("registry_request_invalid")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err = credentialHTTPClient(request.URL).Do(request)
		if err != nil {
			return "", errors.New("registry_manifest_unavailable")
		}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return "", errors.New("registry_manifest_unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) == 0 || len(body) > 1<<20 {
		return "", errors.New("registry_manifest_invalid")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !validRegistryManifestMediaType(mediaType) {
		return "", errors.New("registry_manifest_invalid")
	}
	var manifest struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil || manifest.SchemaVersion != 2 {
		return "", errors.New("registry_manifest_invalid")
	}
	digest := strings.TrimSpace(response.Header.Get("Docker-Content-Digest"))
	if digest == "" {
		return "", errors.New("registry_digest_missing")
	}
	if !validCredentialSHA256Digest(digest) {
		return "", errors.New("registry_digest_invalid")
	}
	computed := sha256.Sum256(body)
	if digest != "sha256:"+hex.EncodeToString(computed[:]) {
		return "", errors.New("registry_digest_mismatch")
	}
	if validCredentialSHA256Digest(reference) && digest != reference {
		return "", errors.New("registry_reference_digest_mismatch")
	}
	return digest, nil
}

func validRegistryManifestMediaType(value string) bool {
	switch strings.TrimSpace(value) {
	case "application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json":
		return true
	default:
		return false
	}
}

func registryManifestRequest(ctx context.Context, endpoint string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(
		"Accept",
		"application/vnd.oci.image.manifest.v1+json, "+
			"application/vnd.oci.image.index.v1+json, "+
			"application/vnd.docker.distribution.manifest.v2+json, "+
			"application/vnd.docker.distribution.manifest.list.v2+json",
	)
	return request, nil
}

func exchangeRegistryBearerToken(
	ctx context.Context,
	registryURL *url.URL,
	challenge string,
	username string,
	password string,
) (string, error) {
	parameters, err := parseRegistryBearerChallenge(challenge)
	if err != nil {
		return "", errors.New("registry_auth_challenge_invalid")
	}
	realm, err := url.Parse(parameters["realm"])
	if err != nil || realm.User != nil || realm.Hostname() == "" || realm.Fragment != "" {
		return "", errors.New("registry_auth_challenge_invalid")
	}
	if realm.Scheme != "https" &&
		!(realm.Scheme == "http" && registryURL.Scheme == "http" && realm.Host == registryURL.Host) {
		return "", errors.New("registry_auth_challenge_invalid")
	}
	query := realm.Query()
	for _, key := range []string{"service", "scope"} {
		if value := parameters[key]; value != "" {
			query.Set(key, value)
		}
	}
	realm.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", errors.New("registry_auth_challenge_invalid")
	}
	request.SetBasicAuth(username, password)
	response, err := credentialHTTPClient(request.URL).Do(request)
	if err != nil {
		return "", errors.New("registry_auth_failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return "", errors.New("registry_auth_failed")
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&payload); err != nil {
		return "", errors.New("registry_auth_response_invalid")
	}
	token := strings.TrimSpace(payload.Token)
	if token == "" {
		token = strings.TrimSpace(payload.AccessToken)
	}
	if token == "" {
		return "", errors.New("registry_auth_response_invalid")
	}
	return token, nil
}

func parseRegistryBearerChallenge(header string) (map[string]string, error) {
	header = strings.TrimSpace(header)
	if len(header) < len("Bearer ") || !strings.EqualFold(header[:len("Bearer")], "Bearer") {
		return nil, errors.New("not a bearer challenge")
	}
	raw := strings.TrimSpace(header[len("Bearer"):])
	parts := make([]string, 0, 3)
	start := 0
	quoted := false
	escaped := false
	for index, char := range raw {
		switch {
		case escaped:
			escaped = false
		case char == '\\' && quoted:
			escaped = true
		case char == '"':
			quoted = !quoted
		case char == ',' && !quoted:
			parts = append(parts, raw[start:index])
			start = index + 1
		}
	}
	if quoted || escaped {
		return nil, errors.New("invalid bearer challenge quoting")
	}
	parts = append(parts, raw[start:])
	parameters := make(map[string]string, len(parts))
	for _, part := range parts {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if !ok || key == "" || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return nil, errors.New("invalid bearer challenge parameter")
		}
		value = strings.ReplaceAll(value[1:len(value)-1], `\"`, `"`)
		if value == "" {
			return nil, errors.New("empty bearer challenge parameter")
		}
		parameters[key] = value
	}
	if parameters["realm"] == "" {
		return nil, errors.New("bearer challenge realm is required")
	}
	return parameters, nil
}

func validateTLSCredential(certificatePEM, privateKeyPEM string) (*x509.Certificate, string, error) {
	certificateBlock, _ := pem.Decode([]byte(certificatePEM))
	keyBlock, _ := pem.Decode([]byte(privateKeyPEM))
	if certificateBlock == nil || keyBlock == nil {
		return nil, "", errors.New("tls_key_pair_invalid")
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		return nil, "", errors.New("tls_key_pair_invalid")
	}
	signer, err := parseTLSPrivateKey(keyBlock)
	if err != nil {
		return nil, "", errors.New("tls_key_pair_invalid")
	}
	certificatePublicKey, certificateErr := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	privatePublicKey, privateErr := x509.MarshalPKIXPublicKey(signer.Public())
	if certificateErr != nil || privateErr != nil || !bytes.Equal(certificatePublicKey, privatePublicKey) {
		return nil, "", errors.New("tls_key_pair_invalid")
	}
	sum := sha256.Sum256(certificate.Raw)
	return certificate, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func parseTLSPrivateKey(block *pem.Block) (crypto.Signer, error) {
	if block == nil {
		return nil, errors.New("private key is missing")
	}
	switch block.Type {
	case "PRIVATE KEY":
		privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		signer, ok := privateKey.(crypto.Signer)
		if !ok {
			return nil, errors.New("private key does not implement crypto.Signer")
		}
		return signer, nil
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported private key PEM type %q", block.Type)
	}
}

func verifyGitRemote(ctx context.Context, repositoryURL, username, token string) (string, error) {
	parsed, err := credentialOutboundURL(repositoryURL)
	if err != nil {
		return "", errors.New("git_remote_invalid")
	}
	if _, err := resolveCredentialHost(ctx, parsed.Hostname()); err != nil {
		return "", errors.New("git_remote_invalid")
	}
	dir, err := os.MkdirTemp("", "doops-git-askpass-*")
	if err != nil {
		return "", errors.New("git_verification_setup_failed")
	}
	defer os.RemoveAll(dir)
	askpass := filepath.Join(dir, "askpass")
	script := "#!/bin/sh\ncase \"$1\" in\n  *sername*) printf '%s' \"$DOOPS_GIT_USERNAME\" ;;\n  *) printf '%s' \"$DOOPS_GIT_SECRET\" ;;\nesac\n"
	if err := os.WriteFile(askpass, []byte(script), 0700); err != nil {
		return "", errors.New("git_verification_setup_failed")
	}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", repositoryURL)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=https",
		"GIT_ASKPASS="+askpass,
		"DOOPS_GIT_USERNAME="+username,
		"DOOPS_GIT_SECRET="+token,
	)
	output, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(output)) == 0 {
		return "", errors.New("git_remote_unavailable")
	}
	sum := sha256.Sum256(bytes.TrimSpace(output))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

var credentialPrivateEndpointAllowed = func(string) bool { return false }

func credentialHTTPClient(endpoint *url.URL) *http.Client {
	host := endpoint.Hostname()
	port := endpoint.Port()
	if port == "" {
		port = "443"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		addresses, err := resolveCredentialHost(ctx, host)
		if err != nil {
			return nil, err
		}
		var dialer net.Dialer
		var lastErr error
		for _, address := range addresses {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func credentialOutboundURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("credential_endpoint_invalid")
	}
	return parsed, nil
}

func resolveCredentialHost(ctx context.Context, host string) ([]net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("credential_endpoint_unresolvable")
	}
	for _, address := range addresses {
		if credentialPrivateEndpointAllowed(host) {
			continue
		}
		if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() ||
			address.IsLinkLocalMulticast() || address.IsUnspecified() || address.IsMulticast() {
			return nil, errors.New("credential_endpoint_private")
		}
	}
	return addresses, nil
}
