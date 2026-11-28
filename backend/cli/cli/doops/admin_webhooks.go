package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type adminWebhookEndpoint struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	URLHost           string   `json:"url_host"`
	URLSHA256         string   `json:"url_sha256"`
	Enabled           bool     `json:"enabled"`
	ScopeCluster      string   `json:"scope_cluster"`
	ScopeInstance     string   `json:"scope_instance"`
	EventTypes        []string `json:"event_types"`
	CreatedBy         string   `json:"created_by"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	LastSuccessAtMs   int64    `json:"last_success_at_ms"`
	LastFailureAtMs   int64    `json:"last_failure_at_ms"`
	LastErrorCategory string   `json:"last_error_category"`
	Secret            string   `json:"secret"`
}

func runAdminWebhooks(args []string, servers []Server, configErr error) {
	if len(args) < 1 {
		adminWebhookUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runAdminWebhookList(args[1:], servers, configErr)
	case "create":
		runAdminWebhookCreate(args[1:], servers, configErr)
	case "show":
		runAdminWebhookShow(args[1:], servers, configErr)
	case "update":
		runAdminWebhookUpdate(args[1:], servers, configErr)
	case "delete":
		runAdminWebhookDelete(args[1:], servers, configErr)
	case "rotate-secret":
		runAdminWebhookRotate(args[1:], servers, configErr)
	case "test":
		runAdminWebhookTest(args[1:], servers, configErr)
	case "deliveries":
		runAdminWebhookDeliveries(args[1:], servers, configErr)
	default:
		adminWebhookUsage()
		os.Exit(1)
	}
}

func adminWebhookUsage() {
	fmt.Println("Usage: doops admin webhook <list|create|show|update|delete|rotate-secret|test|deliveries> ...")
	fmt.Println("       doops admin webhook list --target <gateway-target>")
	fmt.Println("       doops admin webhook create --target <gateway-target> --name <name> --url-file - --event <type>[,<type>...] [--description <text>] [--cluster <cluster>] [--instance <instance>] [--disabled]")
	fmt.Println("       doops admin webhook show --target <gateway-target> --id <webhook-id>")
	fmt.Println("       doops admin webhook update --target <gateway-target> --id <webhook-id> [--name <name>] [--url-file -] [--event <type>[,<type>...]] [--cluster <cluster>] [--instance <instance>] [--enable|--disable]")
	fmt.Println("       doops admin webhook delete --target <gateway-target> --id <webhook-id>")
	fmt.Println("       doops admin webhook rotate-secret --target <gateway-target> --id <webhook-id>")
	fmt.Println("       doops admin webhook test --target <gateway-target> --id <webhook-id>")
	fmt.Println("       doops admin webhook deliveries --target <gateway-target> --id <webhook-id> [--limit <n>]")
}

func runAdminWebhookList(args []string, servers []Server, configErr error) {
	fs := flag.NewFlagSet("admin webhook list", flag.ExitOnError)
	target := fs.String("target", "", "Configured gateway target whose token/gateway should be used")
	fs.Parse(args)
	if strings.TrimSpace(*target) == "" {
		fmt.Println("Error: --target is required")
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, *target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, *target))
		os.Exit(1)
	}
	body, err := GatewayAdminWebhooksList(server.Gateway, mustResolveToken(*server))
	exitOnErr(err)
	var out struct {
		Endpoints []adminWebhookEndpoint `json:"endpoints"`
	}
	exitOnErr(json.Unmarshal(body, &out))
	for _, ep := range out.Endpoints {
		fmt.Printf("id=%s name=%s enabled=%t url_host=%s events=%s scope=%s/%s created_at=%s\n",
			ep.ID, ep.Name, ep.Enabled, ep.URLHost, strings.Join(ep.EventTypes, ","), ep.ScopeCluster, ep.ScopeInstance, ep.CreatedAt)
	}
}

func runAdminWebhookCreate(args []string, servers []Server, configErr error) {
	var target, name, urlFile, events, description, cluster, instance string
	var disabled bool
	fs := flag.NewFlagSet("admin webhook create", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.StringVar(&name, "name", "", "Webhook endpoint name")
	fs.StringVar(&urlFile, "url-file", "", "File containing the HTTPS URL, or - for stdin")
	fs.StringVar(&events, "event", "", "Comma-separated event types")
	fs.StringVar(&description, "description", "", "Optional description")
	fs.StringVar(&cluster, "cluster", "", "Optional cluster scope")
	fs.StringVar(&instance, "instance", "", "Optional instance scope")
	fs.BoolVar(&disabled, "disabled", false, "Create the endpoint disabled")
	fs.Parse(args)
	if strings.TrimSpace(target) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(urlFile) == "" || strings.TrimSpace(events) == "" {
		fmt.Println("Error: --target, --name, --url-file and --event are required")
		adminWebhookUsage()
		os.Exit(1)
	}
	if cluster == "" && instance != "" {
		fmt.Println("Error: --instance requires --cluster")
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	rawURL := readAdminWebhookURL(urlFile)
	body, err := GatewayAdminWebhookCreate(server.Gateway, mustResolveToken(*server), map[string]any{
		"name": name, "description": description, "url": rawURL,
		"event_types": splitWebhookEvents(events), "scope_cluster": cluster, "scope_instance": instance,
		"enabled": !disabled,
	})
	exitOnErr(err)
	var out struct {
		Endpoint adminWebhookEndpoint `json:"endpoint"`
	}
	exitOnErr(json.Unmarshal(body, &out))
	ep := out.Endpoint
	fmt.Printf("id=%s name=%s enabled=%t url_host=%s events=%s\n", ep.ID, ep.Name, ep.Enabled, ep.URLHost, strings.Join(ep.EventTypes, ","))
	fmt.Printf("secret=%s\n", ep.Secret)
	fmt.Println("Store the secret now: it is returned only once and is required to verify X-DoOps-Signature.")
}

func runAdminWebhookShow(args []string, servers []Server, configErr error) {
	target, id, _ := adminWebhookTargetID("admin webhook show", args)
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	body, err := GatewayAdminWebhookGet(server.Gateway, mustResolveToken(*server), id)
	exitOnErr(err)
	var out struct {
		Endpoint adminWebhookEndpoint `json:"endpoint"`
	}
	exitOnErr(json.Unmarshal(body, &out))
	printAdminWebhook(out.Endpoint)
}

func runAdminWebhookUpdate(args []string, servers []Server, configErr error) {
	var target, id, name, urlFile, events, description, cluster, instance string
	var enable, disable bool
	fs := flag.NewFlagSet("admin webhook update", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.StringVar(&id, "id", "", "Webhook endpoint id")
	fs.StringVar(&name, "name", "", "New name")
	fs.StringVar(&urlFile, "url-file", "", "File containing a new HTTPS URL, or - for stdin")
	fs.StringVar(&events, "event", "", "New comma-separated event types")
	fs.StringVar(&description, "description", "", "New description")
	fs.StringVar(&cluster, "cluster", "", "New cluster scope")
	fs.StringVar(&instance, "instance", "", "New instance scope")
	fs.BoolVar(&enable, "enable", false, "Enable the endpoint")
	fs.BoolVar(&disable, "disable", false, "Disable the endpoint")
	fs.Parse(args)
	if strings.TrimSpace(target) == "" || strings.TrimSpace(id) == "" {
		fmt.Println("Error: --target and --id are required")
		os.Exit(1)
	}
	if enable && disable {
		fmt.Println("Error: --enable and --disable are mutually exclusive")
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	payload := map[string]any{}
	if name != "" {
		payload["name"] = name
	}
	if urlFile != "" {
		payload["url"] = readAdminWebhookURL(urlFile)
	}
	if events != "" {
		payload["event_types"] = splitWebhookEvents(events)
	}
	if description != "" {
		payload["description"] = description
	}
	if cluster != "" {
		payload["scope_cluster"] = cluster
	}
	if instance != "" {
		payload["scope_instance"] = instance
	}
	if enable {
		payload["enabled"] = true
	}
	if disable {
		payload["enabled"] = false
	}
	if len(payload) == 0 {
		fmt.Println("Error: nothing to update")
		os.Exit(1)
	}
	body, err := GatewayAdminWebhookUpdate(server.Gateway, mustResolveToken(*server), id, payload)
	exitOnErr(err)
	var out struct {
		Endpoint adminWebhookEndpoint `json:"endpoint"`
	}
	exitOnErr(json.Unmarshal(body, &out))
	printAdminWebhook(out.Endpoint)
}

func runAdminWebhookDelete(args []string, servers []Server, configErr error) {
	target, id, _ := adminWebhookTargetID("admin webhook delete", args)
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	_, err := GatewayAdminWebhookDelete(server.Gateway, mustResolveToken(*server), id)
	exitOnErr(err)
	fmt.Printf("id=%s deleted=true\n", id)
}

func runAdminWebhookRotate(args []string, servers []Server, configErr error) {
	target, id, _ := adminWebhookTargetID("admin webhook rotate-secret", args)
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	body, err := GatewayAdminWebhookRotateSecret(server.Gateway, mustResolveToken(*server), id)
	exitOnErr(err)
	var out struct {
		Secret string `json:"secret"`
	}
	exitOnErr(json.Unmarshal(body, &out))
	fmt.Printf("id=%s secret=%s\n", id, out.Secret)
	fmt.Println("Store the secret now: the previous secret remains valid for 24h.")
}

func runAdminWebhookTest(args []string, servers []Server, configErr error) {
	target, id, _ := adminWebhookTargetID("admin webhook test", args)
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	body, err := GatewayAdminWebhookTest(server.Gateway, mustResolveToken(*server), id)
	exitOnErr(err)
	var out struct {
		EventID string `json:"event_id"`
	}
	exitOnErr(json.Unmarshal(body, &out))
	fmt.Printf("id=%s test_event_id=%s queued=true\n", id, out.EventID)
}

func runAdminWebhookDeliveries(args []string, servers []Server, configErr error) {
	var target, id string
	var limit int
	fs := flag.NewFlagSet("admin webhook deliveries", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.StringVar(&id, "id", "", "Webhook endpoint id")
	fs.IntVar(&limit, "limit", 20, "Maximum deliveries to show")
	fs.Parse(args)
	if strings.TrimSpace(target) == "" || strings.TrimSpace(id) == "" {
		fmt.Println("Error: --target and --id are required")
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	body, err := GatewayAdminWebhookDeliveries(server.Gateway, mustResolveToken(*server), id, limit)
	exitOnErr(err)
	var out struct {
		Deliveries []map[string]any `json:"deliveries"`
	}
	exitOnErr(json.Unmarshal(body, &out))
	encoded, _ := json.MarshalIndent(out.Deliveries, "", "  ")
	fmt.Println(string(encoded))
}

func adminWebhookTargetID(name string, args []string) (string, string, *flag.FlagSet) {
	var target, id string
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.StringVar(&id, "id", "", "Webhook endpoint id")
	fs.Parse(args)
	if strings.TrimSpace(target) == "" || strings.TrimSpace(id) == "" {
		fmt.Println("Error: --target and --id are required")
		os.Exit(1)
	}
	return target, id, fs
}

func readAdminWebhookURL(path string) string {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 8192))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Printf("Error: read --url-file: %v\n", err)
		os.Exit(1)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		fmt.Println("Error: --url-file is empty")
		os.Exit(1)
	}
	return value
}

func splitWebhookEvents(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func printAdminWebhook(ep adminWebhookEndpoint) {
	fmt.Printf("id=%s name=%s enabled=%t url_host=%s url_sha256=%s events=%s scope=%s/%s\n",
		ep.ID, ep.Name, ep.Enabled, ep.URLHost, ep.URLSHA256, strings.Join(ep.EventTypes, ","), ep.ScopeCluster, ep.ScopeInstance)
	if ep.Description != "" {
		fmt.Printf("description=%s\n", ep.Description)
	}
	fmt.Printf("created_at=%s updated_at=%s", ep.CreatedAt, ep.UpdatedAt)
	if ep.LastSuccessAtMs > 0 {
		fmt.Printf(" last_success_at_ms=%d", ep.LastSuccessAtMs)
	}
	if ep.LastFailureAtMs > 0 {
		fmt.Printf(" last_failure_at_ms=%d last_error_category=%s", ep.LastFailureAtMs, ep.LastErrorCategory)
	}
	fmt.Println()
}
