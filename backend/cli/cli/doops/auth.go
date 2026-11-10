package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const authStoreVersion = 2

// GatewayAuth is the local metadata for one Gateway credential. The plaintext
// token is still required for requests, but its identity and lifecycle are
// persisted beside it so a later login cannot look like an unexplained token
// replacement.
type GatewayAuth struct {
	Token     string `json:"token"`
	TokenID   string `json:"token_id,omitempty"`
	Username  string `json:"username,omitempty"`
	Name      string `json:"name,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	IssuedAt  string `json:"issued_at,omitempty"`
	Source    string `json:"source,omitempty"`
}

type AuthStore struct {
	Version     int                    `json:"version"`
	Credentials map[string]GatewayAuth `json:"credentials"`
}

type authStoreDisk struct {
	Version     json.RawMessage `json:"version"`
	Credentials json.RawMessage `json:"credentials"`
	Tokens      json.RawMessage `json:"tokens"`
}

func (s *AuthStore) UnmarshalJSON(data []byte) error {
	var disk authStoreDisk
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&disk); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	if disk.Credentials != nil && disk.Tokens != nil {
		return fmt.Errorf("auth store cannot mix credentials and legacy tokens")
	}
	var credentials map[string]GatewayAuth
	switch {
	case disk.Credentials != nil:
		var version int
		if err := json.Unmarshal(disk.Version, &version); err != nil || version != authStoreVersion {
			return fmt.Errorf("credentials require auth store version %d", authStoreVersion)
		}
		decoder := json.NewDecoder(bytes.NewReader(disk.Credentials))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&credentials); err != nil {
			return err
		}
	case disk.Tokens != nil:
		// The unversioned tokens map was the only previous on-disk format.
		// Convert it here; runtime access and future saves use credentials only.
		if disk.Version != nil {
			return fmt.Errorf("legacy tokens must not declare an auth store version")
		}
		var tokens map[string]string
		if err := json.Unmarshal(disk.Tokens, &tokens); err != nil {
			return err
		}
		if tokens == nil {
			return fmt.Errorf("legacy tokens must be an object")
		}
		credentials = make(map[string]GatewayAuth, len(tokens))
		for key, token := range tokens {
			credentials[key] = GatewayAuth{Token: token, Source: "legacy-auth-json"}
		}
	default:
		return fmt.Errorf("auth store must contain credentials")
	}
	if credentials == nil {
		return fmt.Errorf("credentials must be an object")
	}
	normalized := make(map[string]GatewayAuth, len(credentials))
	for key, credential := range credentials {
		key = gatewayAuthKey(key)
		if key == "" || strings.TrimSpace(credential.Token) == "" {
			return fmt.Errorf("credential must have a Gateway and non-empty token")
		}
		if _, exists := normalized[key]; exists {
			return fmt.Errorf("auth store has conflicting credentials for one normalized Gateway")
		}
		credential.Token = strings.TrimSpace(credential.Token)
		normalized[key] = credential
	}
	s.Version = authStoreVersion
	s.Credentials = normalized
	return nil
}

// resolveLoginGateway picks the Gateway URL for login and logout. A configured
// target resolves its Gateway URL; --gateway allows the first login before any
// local route exists. When both are given they must name the same Gateway.
func resolveLoginGateway(target, gateway string, servers []Server, configErr error) (string, *Server, error) {
	target = strings.TrimSpace(target)
	gateway = strings.TrimSpace(gateway)
	if target == "" && gateway == "" {
		return "", nil, fmt.Errorf("one of --target or --gateway is required; use --gateway <url> for the first login")
	}
	var server *Server
	if target != "" {
		if configErr != nil {
			return "", nil, fmt.Errorf("cannot use --target without a valid local config: %w; use --gateway <url> for the first login", configErr)
		}
		server = findServer(servers, target)
		if server == nil {
			return "", nil, errTargetNotFound(servers, target)
		}
		if gateway == "" {
			gateway = server.Gateway
		} else if gatewayAuthKey(gateway) != gatewayAuthKey(server.Gateway) {
			return "", nil, fmt.Errorf("--gateway %q does not match the Gateway of target %q", gateway, target)
		}
	}
	if _, err := gatewayAuthLoginURL(gateway); err != nil {
		return "", nil, err
	}
	return gateway, server, nil
}

func GetAuthPath() string {
	return filepath.Join(ensureDoopsStateDir(), "auth.json")
}

func LoadAuth() (*AuthStore, error) {
	path := GetAuthPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AuthStore{Version: authStoreVersion, Credentials: make(map[string]GatewayAuth)}, nil
		}
		return nil, fmt.Errorf("read Gateway auth store %s: %w", path, err)
	}

	var store AuthStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parse Gateway auth store %s: %w", path, err)
	}
	return &store, nil
}

func (s *AuthStore) Save() error {
	path := GetAuthPath()
	if s.Version != authStoreVersion || s.Credentials == nil {
		return fmt.Errorf("invalid auth store version or credentials")
	}
	for key, credential := range s.Credentials {
		if key == "" || key != gatewayAuthKey(key) || strings.TrimSpace(credential.Token) == "" {
			return fmt.Errorf("invalid Gateway credential record")
		}
	}
	data, err := json.MarshalIndent(s, "", "    ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".auth-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func gatewayAuthKey(rawGateway string) string {
	rawGateway = strings.TrimSpace(rawGateway)
	if rawGateway == "" {
		return ""
	}
	if !strings.Contains(rawGateway, "://") {
		rawGateway = "https://" + rawGateway
	}
	parsed, err := url.Parse(rawGateway)
	if err != nil {
		return strings.TrimRight(rawGateway, "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "wss":
		parsed.Scheme = "https"
	case "ws":
		parsed.Scheme = "http"
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	parsed.Host = host
	parsed.User = nil
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func (s *AuthStore) GetGatewayRecord(gateway string) GatewayAuth {
	return s.Credentials[gatewayAuthKey(gateway)]
}

func (s *AuthStore) GetGateway(gateway string) string {
	return s.GetGatewayRecord(gateway).Token
}

func (s *AuthStore) SetGatewayRecord(gateway string, credential GatewayAuth) {
	key := gatewayAuthKey(gateway)
	if s.Credentials == nil {
		s.Credentials = make(map[string]GatewayAuth)
	}
	s.Version = authStoreVersion
	s.Credentials[key] = credential
}

func (s *AuthStore) SetGateway(gateway, token string) {
	s.SetGatewayRecord(gateway, GatewayAuth{Token: strings.TrimSpace(token), Source: "manual-token"})
}

func (s *AuthStore) RemoveGateway(gateway string) {
	key := gatewayAuthKey(gateway)
	delete(s.Credentials, key)
}

type gatewayLoginResponse struct {
	Token     string `json:"token"`
	TokenID   string `json:"token_id"`
	TokenType string `json:"token_type"`
	Username  string `json:"username"`
	Name      string `json:"name,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	IssuedAt  string `json:"issued_at,omitempty"`
}

type gatewayAdminTokenCreateRequest struct {
	User    string `json:"user,omitempty"`
	Name    string `json:"name,omitempty"`
	Expires string `json:"expires,omitempty"`
}

type gatewayAdminTokenCreateResponse = gatewayLoginResponse

type gatewayAdminTokenInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	UserName  string `json:"user_name,omitempty"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Revoked   bool   `json:"revoked"`
	ExpiresAt string `json:"expires_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

type gatewayAdminTokensResponse struct {
	Tokens []gatewayAdminTokenInfo `json:"tokens"`
}

func GatewayLogin(gateway, username, password, label string) (GatewayAuth, error) {
	loginURL, err := gatewayAuthLoginURL(gateway)
	if err != nil {
		return GatewayAuth{}, err
	}
	body, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
		"name":     label,
	})
	if err != nil {
		return GatewayAuth{}, err
	}
	req, err := http.NewRequest(http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return GatewayAuth{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return GatewayAuth{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return GatewayAuth{}, decodeGatewayHTTPError("gateway login", resp, respBody)
	}
	var parsed gatewayLoginResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return GatewayAuth{}, err
	}
	if strings.TrimSpace(parsed.Token) == "" {
		return GatewayAuth{}, fmt.Errorf("gateway login returned empty token")
	}
	return parsed.credential(parsed.Token, "password-login")
}

// GatewayCurrentAuth validates an imported token without issuing another token.
func GatewayCurrentAuth(gateway, token, source string) (GatewayAuth, error) {
	endpoint, err := gatewayURLWithPath(gateway, "/v1/auth/me", nil)
	if err != nil {
		return GatewayAuth{}, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return GatewayAuth{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return GatewayAuth{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return GatewayAuth{}, err
	}
	if response.StatusCode != http.StatusOK {
		return GatewayAuth{}, decodeGatewayHTTPError("gateway token validation", response, body)
	}
	var metadata gatewayLoginResponse
	if err := json.Unmarshal(body, &metadata); err != nil {
		return GatewayAuth{}, fmt.Errorf("decode Gateway token metadata: %w", err)
	}
	return metadata.credential(token, source)
}

func (r gatewayLoginResponse) credential(token, source string) (GatewayAuth, error) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(r.TokenID) == "" || strings.TrimSpace(r.Username) == "" || strings.TrimSpace(r.IssuedAt) == "" {
		return GatewayAuth{}, fmt.Errorf("Gateway authentication response omitted token metadata")
	}
	for _, value := range []string{r.IssuedAt, r.ExpiresAt} {
		if value != "" {
			stamp, err := time.Parse(time.RFC3339Nano, value)
			if err != nil || stamp.IsZero() {
				return GatewayAuth{}, fmt.Errorf("Gateway authentication response returned an invalid token timestamp")
			}
		}
	}
	return GatewayAuth{
		Token:     strings.TrimSpace(token),
		TokenID:   strings.TrimSpace(r.TokenID),
		Username:  strings.TrimSpace(r.Username),
		Name:      strings.TrimSpace(r.Name),
		ExpiresAt: r.ExpiresAt,
		IssuedAt:  r.IssuedAt,
		Source:    source,
	}, nil
}

func GatewayAdminTokenCreate(gateway, adminToken string, create gatewayAdminTokenCreateRequest) (gatewayAdminTokenCreateResponse, error) {
	create.User = strings.TrimSpace(create.User)
	create.Name = strings.TrimSpace(create.Name)
	create.Expires = strings.TrimSpace(create.Expires)
	if create.User == "" {
		return gatewayAdminTokenCreateResponse{}, fmt.Errorf("user is required")
	}
	createURL, err := gatewayAdminTokenCreateURL(gateway)
	if err != nil {
		return gatewayAdminTokenCreateResponse{}, err
	}
	body, err := json.Marshal(create)
	if err != nil {
		return gatewayAdminTokenCreateResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPost, createURL, bytes.NewReader(body))
	if err != nil {
		return gatewayAdminTokenCreateResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(adminToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(adminToken))
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return gatewayAdminTokenCreateResponse{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return gatewayAdminTokenCreateResponse{}, decodeGatewayHTTPError("gateway admin token create", resp, respBody)
	}
	var parsed gatewayAdminTokenCreateResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return gatewayAdminTokenCreateResponse{}, err
	}
	if strings.TrimSpace(parsed.Token) == "" {
		return gatewayAdminTokenCreateResponse{}, fmt.Errorf("gateway admin token create returned empty token")
	}
	return parsed, nil
}

func GatewayAdminTokensList(gateway, adminToken, kind string) ([]gatewayAdminTokenInfo, error) {
	listURL, err := gatewayAdminTokensURL(gateway, strings.TrimSpace(kind), "")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(adminToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(adminToken))
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, decodeGatewayHTTPError("gateway admin token list", resp, respBody)
	}
	var parsed gatewayAdminTokensResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	return parsed.Tokens, nil
}

func GatewayAdminTokenRevoke(gateway, adminToken, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("token id is required")
	}
	revokeURL, err := gatewayAdminTokensURL(gateway, "", id)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, revokeURL, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(adminToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(adminToken))
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return decodeGatewayHTTPError("gateway admin token revoke", resp, respBody)
	}
	return nil
}

func gatewayAuthLoginURL(rawGateway string) (string, error) {
	return gatewayURLWithPath(rawGateway, "/v1/auth/login", nil)
}

func gatewayAdminTokenCreateURL(rawGateway string) (string, error) {
	return gatewayURLWithPath(rawGateway, "/v1/admin/tokens", nil)
}

func gatewayAdminTokensURL(rawGateway, kind, id string) (string, error) {
	values := url.Values{}
	if strings.TrimSpace(kind) != "" {
		values.Set("kind", strings.TrimSpace(kind))
	}
	if strings.TrimSpace(id) != "" {
		values.Set("id", strings.TrimSpace(id))
	}
	return gatewayURLWithPath(rawGateway, "/v1/admin/tokens", values)
}
