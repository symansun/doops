package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type GatewayAdminInstance struct {
	Cluster    string `json:"cluster"`
	Instance   string `json:"instance"`
	Status     string `json:"status"`
	Remote     string `json:"remote,omitempty"`
	Busy       bool   `json:"busy"`
	ActiveOps  int    `json:"active_ops"`
	QueuedOps  int    `json:"queued_ops"`
	LastSeen   string `json:"last_seen,omitempty"`
	Archived   bool   `json:"archived"`
	ArchivedAt string `json:"archived_at,omitempty"`
	ArchivedBy string `json:"archived_by,omitempty"`
}

type GatewayAdminInstanceDeleteResult struct {
	Cluster   string `json:"cluster"`
	Instance  string `json:"instance"`
	Deleted   bool   `json:"deleted"`
	Remaining struct {
		ActiveCredentialGrants   int `json:"active_credential_grants"`
		RevokingCredentialGrants int `json:"revoking_credential_grants"`
		ProxyRoutes              int `json:"proxy_routes"`
		UserGrants               int `json:"user_grants"`
	} `json:"remaining"`
}

func GatewayAdminInstancesList(gateway, adminToken string, includeHistory bool) ([]GatewayAdminInstance, error) {
	var query url.Values
	if includeHistory {
		query = url.Values{"include": []string{"history"}}
	}
	body, err := gatewayAdminRequest(http.MethodGet, gateway, adminToken, "/v1/admin/instances", query, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Instances []GatewayAdminInstance `json:"instances"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode admin instances: %w", err)
	}
	return resp.Instances, nil
}

func GatewayAdminInstanceArchive(gateway, adminToken, cluster, instance string, unarchive bool) (GatewayAdminInstance, error) {
	path := "/v1/admin/instances/archive"
	if unarchive {
		path = "/v1/admin/instances/unarchive"
	}
	payload, err := json.Marshal(map[string]string{
		"cluster":  strings.TrimSpace(cluster),
		"instance": strings.TrimSpace(instance),
	})
	if err != nil {
		return GatewayAdminInstance{}, err
	}
	body, err := gatewayAdminRequest(http.MethodPost, gateway, adminToken, path, nil, payload)
	if err != nil {
		return GatewayAdminInstance{}, err
	}
	var resp GatewayAdminInstance
	if err := json.Unmarshal(body, &resp); err != nil {
		return GatewayAdminInstance{}, fmt.Errorf("decode admin instance: %w", err)
	}
	return resp, nil
}

func GatewayAdminInstanceDelete(gateway, adminToken, cluster, instance string) (GatewayAdminInstanceDeleteResult, error) {
	payload, err := json.Marshal(map[string]string{
		"cluster":  strings.TrimSpace(cluster),
		"instance": strings.TrimSpace(instance),
	})
	if err != nil {
		return GatewayAdminInstanceDeleteResult{}, err
	}
	body, err := gatewayAdminRequest(http.MethodPost, gateway, adminToken, "/v1/admin/instances/delete", nil, payload)
	if err != nil {
		return GatewayAdminInstanceDeleteResult{}, err
	}
	var resp GatewayAdminInstanceDeleteResult
	if err := json.Unmarshal(body, &resp); err != nil {
		return GatewayAdminInstanceDeleteResult{}, fmt.Errorf("decode admin instance delete: %w", err)
	}
	return resp, nil
}

func runAdminInstances(args []string, servers []Server, configErr error) {
	if err := runAdminInstancesWithWriter(args, servers, configErr, os.Stdout); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func runAdminInstancesWithWriter(args []string, servers []Server, configErr error, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printAdminInstanceUsage(stdout)
		if len(args) == 0 {
			return fmt.Errorf("admin instance subcommand is required")
		}
		return nil
	}
	switch args[0] {
	case "list":
		return runAdminInstanceList(args[1:], servers, configErr, stdout)
	case "archive":
		return runAdminInstanceArchive(args[1:], servers, configErr, stdout, false)
	case "unarchive":
		return runAdminInstanceArchive(args[1:], servers, configErr, stdout, true)
	case "delete", "rm":
		return runAdminInstanceDelete(args[1:], servers, configErr, stdout)
	default:
		printAdminInstanceUsage(stdout)
		return fmt.Errorf("unknown admin instance subcommand %q", args[0])
	}
}

func printAdminInstanceUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: doops admin instance list --target <gateway-target> [--include-history]")
	fmt.Fprintln(w, "       doops admin instance archive --target <gateway-target> --cluster <cluster> --instance <instance>")
	fmt.Fprintln(w, "       doops admin instance unarchive --target <gateway-target> --cluster <cluster> --instance <instance>")
	fmt.Fprintln(w, "       doops admin instance delete --target <gateway-target> --cluster <cluster> --instance <instance>")
}

func runAdminInstanceList(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target string
	var includeHistory bool
	fs := flag.NewFlagSet("admin instance list", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.BoolVar(&includeHistory, "include-history", false, "Include offline history and archived instances")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	instances, err := GatewayAdminInstancesList(gateway, token, includeHistory)
	if err != nil {
		return err
	}
	for _, item := range instances {
		fmt.Fprintf(stdout, "cluster=%s instance=%s status=%s archived=%t last_seen=%s remote=%s\n",
			item.Cluster, item.Instance, item.Status, item.Archived, item.LastSeen, item.Remote)
	}
	return nil
}

func runAdminInstanceArchive(args []string, servers []Server, configErr error, stdout io.Writer, unarchive bool) error {
	name := "admin instance archive"
	if unarchive {
		name = "admin instance unarchive"
	}
	var target, cluster, instance string
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.StringVar(&cluster, "cluster", "", "Target cluster name")
	fs.StringVar(&instance, "instance", "", "Target instance name")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if strings.TrimSpace(cluster) == "" || strings.TrimSpace(instance) == "" {
		return fmt.Errorf("--cluster and --instance are required")
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	item, err := GatewayAdminInstanceArchive(gateway, token, cluster, instance, unarchive)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "cluster=%s instance=%s status=%s archived=%t\n",
		item.Cluster, item.Instance, item.Status, item.Archived)
	return nil
}

func runAdminInstanceDelete(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, cluster, instance string
	fs := flag.NewFlagSet("admin instance delete", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.StringVar(&cluster, "cluster", "", "Offline target cluster name to decommission")
	fs.StringVar(&instance, "instance", "", "Offline target instance name to decommission")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if strings.TrimSpace(cluster) == "" || strings.TrimSpace(instance) == "" {
		return fmt.Errorf("--cluster and --instance are required")
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	res, err := GatewayAdminInstanceDelete(gateway, token, cluster, instance)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "cluster=%s instance=%s deleted=%t remaining_active_credential_grants=%d remaining_revoking_credential_grants=%d remaining_proxy_routes=%d remaining_user_grants=%d\n",
		res.Cluster, res.Instance, res.Deleted,
		res.Remaining.ActiveCredentialGrants,
		res.Remaining.RevokingCredentialGrants,
		res.Remaining.ProxyRoutes,
		res.Remaining.UserGrants)
	return nil
}
