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
	"strconv"
	"strings"
	"time"
)

type AuthStore struct {
	Tokens map[string]string `json:"tokens"`
}

func GetAuthPath() string {
	return filepath.Join(ensureDoopsStateDir(), "auth.json")
}

func LoadAuth() (*AuthStore, error) {
	path := GetAuthPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AuthStore{Tokens: make(map[string]string)}, nil
		}
		return nil, fmt.Errorf("read Gateway auth store %s: %w", path, err)
	}

	var store AuthStore
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store); err != nil {
		return nil, fmt.Errorf("parse Gateway auth store %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse Gateway auth store %s: trailing JSON data", path)
	}

	if store.Tokens == nil {
		store.Tokens = make(map[string]string)
	}
	return &store, nil
}

func (s *AuthStore) Save() error {
	path := GetAuthPath()
	data, err := json.MarshalIndent(s, "", "    ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
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

func (s *AuthStore) GetGateway(gateway string) string {
	return s.Tokens[gatewayAuthKey(gateway)]
}

func (s *AuthStore) SetGateway(gateway, token string) {
	if s.Tokens == nil {
		s.Tokens = make(map[string]string)
	}
	s.Tokens[gatewayAuthKey(gateway)] = token
}

func (s *AuthStore) RemoveGateway(gateway string) {
	delete(s.Tokens, gatewayAuthKey(gateway))
}

type gatewayLoginResponse struct {
	Token    string `json:"token"`
	TokenID  string `json:"token_id"`
	Username string `json:"username"`
}

type gatewayAdminTokenCreateRequest struct {
	User    string `json:"user,omitempty"`
	Name    string `json:"name,omitempty"`
	Expires string `json:"expires,omitempty"`
}

type gatewayAdminTokenCreateResponse struct {
	Token     string `json:"token"`
	TokenID   string `json:"token_id"`
	TokenType string `json:"token_type"`
	Username  string `json:"username"`
}

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

type gatewayActiveOperation struct {
	ID             string   `json:"id"`
	AuditID        int64    `json:"audit_id,omitempty"`
	State          string   `json:"state"`
	Resource       string   `json:"resource,omitempty"`
	BlockedBy      []string `json:"blocked_by,omitempty"`
	UserID         string   `json:"user_id"`
	TokenID        string   `json:"token_id,omitempty"`
	Cluster        string   `json:"cluster"`
	Instance       string   `json:"instance"`
	Action         string   `json:"action"`
	Session        string   `json:"session,omitempty"`
	CommandSummary string   `json:"command_summary,omitempty"`
	Kind           string   `json:"kind"`
	StartedAt      string   `json:"started_at"`
	AgeSeconds     int64    `json:"age_seconds"`
}

type gatewayAdminOperationsResponse struct {
	Operations []gatewayActiveOperation `json:"operations"`
}

type gatewayAdminOperationReconcileRequest struct {
	ID             string `json:"id"`
	ConfirmStopped bool   `json:"confirm_stopped"`
	Reason         string `json:"reason"`
	Evidence       string `json:"evidence"`
}

func (r *gatewayAdminOperationReconcileRequest) validate() error {
	r.ID = strings.TrimSpace(r.ID)
	r.Reason = strings.TrimSpace(r.Reason)
	r.Evidence = strings.TrimSpace(r.Evidence)
	auditID, err := strconv.ParseInt(strings.TrimPrefix(r.ID, "audit-"), 10, 64)
	if !strings.HasPrefix(r.ID, "audit-") || err != nil || auditID <= 0 {
		return fmt.Errorf("--id must be an audit-N operation id with a positive audit number")
	}
	if !r.ConfirmStopped {
		return fmt.Errorf("--confirm-stopped is required after human review confirms the operation has stopped")
	}
	if r.Reason == "" {
		return fmt.Errorf("--reason is required for manual reconciliation")
	}
	if r.Evidence == "" {
		return fmt.Errorf("--evidence is required for human-reviewed stopped state")
	}
	return nil
}

func GatewayLogin(gateway, username, password, label string) (string, error) {
	loginURL, err := gatewayAuthLoginURL(gateway)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
		"name":     label,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", decodeGatewayLoginHTTPError(resp, respBody)
	}
	var parsed gatewayLoginResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Token) == "" {
		return "", fmt.Errorf("gateway login returned empty token")
	}
	return parsed.Token, nil
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

func GatewayAdminOperationsList(gateway, adminToken, cluster, instance string) ([]gatewayActiveOperation, error) {
	opsURL, err := gatewayAdminOperationsURL(gateway, "", cluster, instance)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, opsURL, nil)
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
		return nil, decodeGatewayHTTPError("gateway admin operations list", resp, respBody)
	}
	var parsed gatewayAdminOperationsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	return parsed.Operations, nil
}

func GatewayAdminOperationCancel(gateway, adminToken, id, cluster, instance string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("operation id is required")
	}
	cancelURL, err := gatewayAdminOperationsURL(gateway, id, cluster, instance)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, cancelURL, nil)
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
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusAccepted {
		return decodeGatewayHTTPError("gateway admin operation cancel", resp, respBody)
	}
	var parsed struct {
		ID              string `json:"id"`
		CancelRequested bool   `json:"cancel_requested"`
		State           string `json:"state"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return fmt.Errorf("decode gateway admin operation cancel response: %w", err)
	}
	if parsed.ID != id || !parsed.CancelRequested || parsed.State != "cancel_requested" {
		return fmt.Errorf("gateway admin operation cancel returned invalid acknowledgment: expected id=%s cancel_requested=true state=cancel_requested", id)
	}
	return nil
}

func GatewayAdminOperationReconcile(gateway, adminToken, cluster, instance string, reconcile gatewayAdminOperationReconcileRequest) error {
	if err := reconcile.validate(); err != nil {
		return err
	}
	reconcileURL, err := gatewayAdminOperationsURL(gateway, "", cluster, instance)
	if err != nil {
		return err
	}
	body, err := json.Marshal(reconcile)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, reconcileURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(adminToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(adminToken))
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return decodeGatewayHTTPError("gateway admin operation reconcile", resp, respBody)
	}
	var parsed struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return fmt.Errorf("decode gateway admin operation reconcile response: %w", err)
	}
	if parsed.ID != reconcile.ID || parsed.State != "resolved_manually" {
		return fmt.Errorf("gateway admin operation reconcile returned invalid acknowledgment: expected id=%s state=resolved_manually", reconcile.ID)
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

func gatewayAdminOperationsURL(rawGateway, id, cluster, instance string) (string, error) {
	values := url.Values{}
	if strings.TrimSpace(id) != "" {
		values.Set("id", strings.TrimSpace(id))
	}
	if strings.TrimSpace(cluster) != "" {
		values.Set("cluster", strings.TrimSpace(cluster))
	}
	if strings.TrimSpace(instance) != "" {
		values.Set("instance", strings.TrimSpace(instance))
	}
	return gatewayURLWithPath(rawGateway, "/v1/admin/operations", values)
}
