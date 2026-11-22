package main

import (
	"fmt"
	"io"
	"strings"
)

func printServerList(w io.Writer, servers []Server) error {
	if err := validateGatewayServers(servers); err != nil {
		return err
	}
	fmt.Fprintf(w, "%-20s %-18s %-28s %-30s %-30s\n", "NAME", "ALIASES", "GATEWAY", "CLUSTER/INSTANCE", "USE")
	fmt.Fprintln(w, strings.Repeat("-", 130))
	for _, s := range servers {
		fmt.Fprintf(w, "%-20s %-18s %-28s %-30s %-30s\n",
			s.Name,
			strings.Join(s.Aliases, ","),
			s.Gateway,
			strings.TrimSpace(s.Cluster)+"/"+strings.TrimSpace(s.Instance),
			s.Use,
		)
	}
	return nil
}

func gatewayTargetUnavailableMessage(cluster, instance string) string {
	return fmt.Sprintf(
		"Error: target unavailable: %s/%s is not visible through this Gateway login; it may be offline or the user may lack an explicit target grant. Check the Gateway token, exact target route, and grant before concluding the cluster is down.",
		strings.TrimSpace(cluster), strings.TrimSpace(instance),
	)
}

func findServer(servers []Server, name string) *Server {
	for _, s := range servers {
		if serverMatchesTarget(s, name) {
			return &s
		}
	}
	return nil
}

func serverMatchesTarget(server Server, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if server.Name == target {
		return true
	}
	for _, alias := range server.Aliases {
		if alias == target {
			return true
		}
	}
	return false
}

func findAliasConflict(servers []Server, candidate Server) string {
	names := append([]string{candidate.Name}, candidate.Aliases...)
	for _, server := range servers {
		if server.Name == candidate.Name {
			continue
		}
		for _, name := range names {
			if serverMatchesTarget(server, name) {
				return server.Name
			}
		}
	}
	return ""
}

func findRouteConflict(servers []Server, candidate Server) string {
	candidateGateway := strings.TrimRight(strings.TrimSpace(candidate.Gateway), "/")
	candidateCluster := strings.TrimSpace(candidate.Cluster)
	candidateInstance := strings.TrimSpace(candidate.Instance)
	if candidateGateway == "" || candidateCluster == "" || candidateInstance == "" {
		return ""
	}
	for _, server := range servers {
		if server.Name == candidate.Name {
			continue
		}
		if strings.TrimRight(strings.TrimSpace(server.Gateway), "/") == candidateGateway &&
			strings.TrimSpace(server.Cluster) == candidateCluster &&
			strings.TrimSpace(server.Instance) == candidateInstance {
			return server.Name
		}
	}
	return ""
}
