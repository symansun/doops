package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type gatewayProxyRoute struct {
	ID       string `json:"id,omitempty"`
	Host     string `json:"host"`
	Cluster  string `json:"cluster"`
	Instance string `json:"instance"`
	Upstream string `json:"upstream"`
	Enabled  *bool  `json:"enabled,omitempty"`
	// UpdatedAt is sent back unchanged so the Gateway refuses the edit with 409
	// if another admin changed the route after this read.
	UpdatedAt string `json:"updated_at,omitempty"`
}

type gatewayProxyRoutesResponse struct {
	Routes []gatewayProxyRoute `json:"routes"`
}

// proxyRouteChange holds only the fields an operator passed; empty means keep.
type proxyRouteChange struct {
	Host, Cluster, Instance, Upstream string
	Enabled                           *bool
}

func gatewayProxyRoutesList(gateway, token string) ([]gatewayProxyRoute, error) {
	raw, err := gatewayAdminRequest("GET", gateway, token, "/v1/admin/proxy-routes", nil, nil)
	if err != nil {
		return nil, err
	}
	var parsed gatewayProxyRoutesResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed.Routes, nil
}

func gatewayProxyRouteUpsert(gateway, token string, route gatewayProxyRoute) (gatewayProxyRoute, error) {
	body, err := json.Marshal(route)
	if err != nil {
		return gatewayProxyRoute{}, err
	}
	raw, err := gatewayAdminRequest("POST", gateway, token, "/v1/admin/proxy-routes", nil, body)
	if err != nil {
		return gatewayProxyRoute{}, err
	}
	var saved gatewayProxyRoute
	if err := json.Unmarshal(raw, &saved); err != nil {
		return gatewayProxyRoute{}, err
	}
	return saved, nil
}

func gatewayProxyRouteDelete(gateway, token, id string) error {
	_, err := gatewayAdminRequest("DELETE", gateway, token, "/v1/admin/proxy-routes",
		url.Values{"id": []string{id}}, nil)
	return err
}

// mergeProxyRoute applies a change to an existing route. The Gateway upsert
// replaces every field, so an in-place edit must resend the unchanged ones.
func mergeProxyRoute(existing gatewayProxyRoute, change proxyRouteChange) gatewayProxyRoute {
	merged := existing
	for _, field := range []struct {
		dst *string
		src string
	}{
		{&merged.Host, change.Host}, {&merged.Cluster, change.Cluster},
		{&merged.Instance, change.Instance}, {&merged.Upstream, change.Upstream},
	} {
		if strings.TrimSpace(field.src) != "" {
			*field.dst = strings.TrimSpace(field.src)
		}
	}
	if change.Enabled != nil {
		merged.Enabled = change.Enabled
	}
	return merged
}

func findProxyRoute(routes []gatewayProxyRoute, id string) (gatewayProxyRoute, bool) {
	for _, route := range routes {
		if route.ID == id {
			return route, true
		}
	}
	return gatewayProxyRoute{}, false
}

func runAdminProxyRoutes(args []string, servers []Server, configErr error) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printAdminProxyUsage()
		if len(args) == 0 {
			os.Exit(1)
		}
		return
	}
	switch args[0] {
	case "list":
		var target string
		fs := flag.NewFlagSet("admin proxy list", flag.ExitOnError)
		fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
		fs.Parse(args[1:])
		gateway, token := applyTarget(target, servers, configErr)
		routes, err := gatewayProxyRoutesList(gateway, token)
		exitOnErr(err)
		for _, route := range routes {
			enabled := route.Enabled == nil || *route.Enabled
			fmt.Printf("id=%s host=%s cluster=%s instance=%s upstream=%s enabled=%v\n",
				route.ID, route.Host, route.Cluster, route.Instance, route.Upstream, enabled)
		}
	case "set":
		runAdminProxySet(args[1:], servers, configErr)
	case "delete", "rm":
		var target, id string
		fs := flag.NewFlagSet("admin proxy delete", flag.ExitOnError)
		fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
		fs.StringVar(&id, "id", "", "Proxy route id")
		fs.Parse(args[1:])
		if strings.TrimSpace(id) == "" {
			fmt.Println("Error: --id is required")
			os.Exit(1)
		}
		gateway, token := applyTarget(target, servers, configErr)
		exitOnErr(gatewayProxyRouteDelete(gateway, token, strings.TrimSpace(id)))
		fmt.Printf("proxy_route_id=%s deleted=true\n", strings.TrimSpace(id))
	default:
		printAdminProxyUsage()
		os.Exit(1)
	}
}

func runAdminProxySet(args []string, servers []Server, configErr error) {
	var target, id, host, scopeTarget, upstream, enabledRaw string
	fs := flag.NewFlagSet("admin proxy set", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
	fs.StringVar(&id, "id", "", "Existing route id; omit to create a route")
	fs.StringVar(&host, "host", "", "Public hostname served by the Gateway")
	fs.StringVar(&scopeTarget, "scope-target", "", "Configured target whose cluster/instance receives the traffic")
	fs.StringVar(&upstream, "upstream", "", "http(s) upstream URL reachable from that Agent")
	fs.StringVar(&enabledRaw, "enabled", "", "true or false; omit to keep the current value")
	fs.Parse(args)

	gateway, token := applyTarget(target, servers, configErr)
	change := proxyRouteChange{Host: host, Upstream: upstream}
	if strings.TrimSpace(scopeTarget) != "" {
		scope := findServer(servers, scopeTarget)
		if scope == nil {
			fmt.Printf("Error: scope target %q not found\n", scopeTarget)
			os.Exit(1)
		}
		if gatewayAuthKey(scope.Gateway) != gatewayAuthKey(gateway) {
			fmt.Println("Error: --scope-target belongs to a different Gateway")
			os.Exit(1)
		}
		change.Cluster, change.Instance = scope.Cluster, scope.Instance
	}
	switch strings.TrimSpace(enabledRaw) {
	case "":
	case "true", "false":
		value := strings.TrimSpace(enabledRaw) == "true"
		change.Enabled = &value
	default:
		fmt.Println("Error: --enabled must be true or false")
		os.Exit(1)
	}

	route := mergeProxyRoute(gatewayProxyRoute{}, change)
	if id = strings.TrimSpace(id); id != "" {
		routes, err := gatewayProxyRoutesList(gateway, token)
		exitOnErr(err)
		existing, ok := findProxyRoute(routes, id)
		if !ok {
			fmt.Printf("Error: proxy route %q not found\n", id)
			os.Exit(1)
		}
		route = mergeProxyRoute(existing, change)
	} else if route.Host == "" || route.Cluster == "" || route.Upstream == "" {
		fmt.Println("Error: a new route requires --host, --scope-target and --upstream")
		os.Exit(1)
	}
	saved, err := gatewayProxyRouteUpsert(gateway, token, route)
	exitOnErr(err)
	enabled := saved.Enabled == nil || *saved.Enabled
	fmt.Printf("proxy_route_id=%s host=%s cluster=%s instance=%s upstream=%s enabled=%v\n",
		saved.ID, saved.Host, saved.Cluster, saved.Instance, saved.Upstream, enabled)
}

func printAdminProxyUsage() {
	fmt.Println("Usage: doops admin proxy list --target <gateway-target>")
	fmt.Println("       doops admin proxy set --target <gateway-target> --host <hostname> --scope-target <target> --upstream <http-url> [--enabled=true|false]")
	fmt.Println("       doops admin proxy set --target <gateway-target> --id <route-id> [--host ...] [--scope-target ...] [--upstream ...] [--enabled=true|false]")
	fmt.Println("       doops admin proxy delete --target <gateway-target> --id <route-id>")
	fmt.Println("With --id, only the passed fields change; the rest keep their current values.")
}
