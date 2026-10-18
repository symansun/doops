package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type gatewayTargetsResponse struct {
	Targets []GatewayTarget `json:"targets"`
}

type GatewayTarget struct {
	Cluster       string    `json:"cluster"`
	Instance      string    `json:"instance"`
	Key           string    `json:"key"`
	Remote        string    `json:"remote"`
	ReleaseCommit string    `json:"release_commit"`
	ConnectedAt   time.Time `json:"connected_at"`
	LastSeen      time.Time `json:"last_seen"`
	Busy          bool      `json:"busy"`
	Status        string    `json:"status"`
	BusyReason    string    `json:"busy_reason"`
	ActiveOps     int       `json:"active_ops"`
	QueuedOps     int       `json:"queued_ops"`
	Resources     []string  `json:"resources"`
	Sessions      []string  `json:"sessions"`
}

func fetchGatewayTargets(gateway, token string) ([]GatewayTarget, error) {
	targetsURL, err := gatewayTargetsURL(gateway)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, targetsURL, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, decodeGatewayHTTPError("gateway targets", resp, body)
	}
	var parsed gatewayTargetsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Targets, nil
}

func filterGatewayTargets(targets []GatewayTarget, cluster, instance string) []GatewayTarget {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	filtered := make([]GatewayTarget, 0, 1)
	for _, target := range targets {
		if target.Cluster == cluster && target.Instance == instance {
			filtered = append(filtered, target)
		}
	}
	return filtered
}

func probeGatewayTarget(server Server, token, session string) (bool, error) {
	// The removal audit must use the explicit session, not a cached Agent session.
	client := NewMCPClient(server, &SessionStore{}, session, false)
	client.Token = token
	defer client.Close()

	_, err := client.CallAndCapture("doops_node_info", map[string]interface{}{})
	if err == nil {
		return true, nil
	}
	var rpcErr *gatewayRPCError
	offline := fmt.Sprintf(
		"target offline: %s/%s",
		strings.TrimSpace(server.Cluster),
		strings.TrimSpace(server.Instance),
	)
	// Gateway checks exact target/action authorization before returning -32004.
	if errors.As(err, &rpcErr) && rpcErr.Code == -32004 &&
		rpcErr.Message == offline {
		return false, nil
	}
	return false, err
}

func unlockGatewayTarget(gateway, token, cluster, instance string) error {
	unlockURL, err := gatewayTargetUnlockURL(gateway, cluster, instance)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, unlockURL, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return decodeGatewayHTTPError("gateway unlock", resp, body)
	}
	return nil
}

func gatewayTargetsURL(rawGateway string) (string, error) {
	return gatewayURLWithPath(rawGateway, "/v1/targets", nil)
}

func gatewayTargetUnlockURL(rawGateway, cluster, instance string) (string, error) {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if cluster == "" || instance == "" {
		return "", fmt.Errorf("cluster and instance are required")
	}
	return gatewayURLWithPath(rawGateway, "/v1/targets/unlock", url.Values{
		"cluster":  []string{cluster},
		"instance": []string{instance},
	})
}

func gatewayURLWithPath(rawGateway, endpoint string, query url.Values) (string, error) {
	rawGateway = strings.TrimSpace(rawGateway)
	if rawGateway == "" {
		return "", fmt.Errorf("gateway URL is required")
	}
	if !strings.Contains(rawGateway, "://") {
		rawGateway = "https://" + rawGateway
	}
	u, err := url.Parse(rawGateway)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http", "https":
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return "", fmt.Errorf("unsupported gateway scheme %q", u.Scheme)
	}
	if err := enforceSecureGatewayURL(rawGateway, u); err != nil {
		return "", err
	}
	u.Path = joinURLPath(u.Path, strings.TrimPrefix(endpoint, "/"))
	if query == nil {
		u.RawQuery = ""
	} else {
		u.RawQuery = query.Encode()
	}
	return u.String(), nil
}

func joinURLPath(base string, elems ...string) string {
	parts := make([]string, 0, len(elems)+1)
	if trimmed := strings.Trim(base, "/"); trimmed != "" {
		parts = append(parts, trimmed)
	}
	for _, elem := range elems {
		if trimmed := strings.Trim(elem, "/"); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

// enforceSecureGatewayURL rejects non-TLS gateway URLs (http:// / ws://) unless
// the host is a loopback address or the operator explicitly opts in via
// DOOPS_ALLOW_INSECURE_GATEWAY=1. This mirrors the agent-side policy so the CLI
// and agent stay consistent.
func enforceSecureGatewayURL(raw string, u *url.URL) error {
	if u == nil {
		return fmt.Errorf("gateway URL is required")
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		return nil
	}
	if isLoopbackHost(u.Hostname()) {
		return nil
	}
	if strings.TrimSpace(os.Getenv("DOOPS_ALLOW_INSECURE_GATEWAY")) == "1" {
		return nil
	}
	return fmt.Errorf("insecure gateway URL %q rejected: use https:// or wss:// (for example https://doops.l8ai.cn), or set DOOPS_ALLOW_INSECURE_GATEWAY=1 to override (loopback hosts are always allowed)", strings.TrimSpace(raw))
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
