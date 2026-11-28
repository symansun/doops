package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// mergeLiveGatewayRoutes adds local routes for live Gateway targets that this
// login can already see but that are not configured yet. Existing names,
// aliases and descriptions stay as they are. Targets that are offline are not
// in the live list, so their local routes are left for `doops remove`.
func mergeLiveGatewayRoutes(servers []Server, gateway string, live []GatewayTarget) ([]Server, []Server) {
	updated := append([]Server(nil), servers...)
	gateway = strings.TrimSpace(gateway)
	candidates := append([]GatewayTarget(nil), live...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Cluster == candidates[j].Cluster {
			return candidates[i].Instance < candidates[j].Instance
		}
		return candidates[i].Cluster < candidates[j].Cluster
	})
	var added []Server
	for _, target := range candidates {
		cluster := strings.TrimSpace(target.Cluster)
		instance := strings.TrimSpace(target.Instance)
		if cluster == "" || instance == "" || cluster == "*" || instance == "*" {
			continue
		}
		if findSameRoute(updated, gateway, cluster, instance) != "" {
			continue
		}
		server := Server{
			Name:     uniqueRouteName(updated, cluster, instance),
			Use:      "Synced from Gateway",
			Gateway:  gateway,
			Cluster:  cluster,
			Instance: instance,
		}
		if findAliasConflict(updated, server) != "" || findRouteConflict(updated, server) != "" {
			continue
		}
		updated = append(updated, server)
		added = append(added, server)
	}
	return updated, added
}

func findSameRoute(servers []Server, gateway, cluster, instance string) string {
	key := gatewayAuthKey(gateway)
	for _, server := range servers {
		if gatewayAuthKey(server.Gateway) == key &&
			strings.TrimSpace(server.Cluster) == cluster &&
			strings.TrimSpace(server.Instance) == instance {
			return server.Name
		}
	}
	return ""
}

func uniqueRouteName(servers []Server, cluster, instance string) string {
	primary := sanitizeRouteName(instance)
	fallback := sanitizeRouteName(cluster + "-" + instance)
	for _, candidate := range []string{primary, fallback} {
		if routeNameFree(servers, candidate) {
			return candidate
		}
	}
	base := fallback
	if base == "" {
		base = "gateway-target"
	}
	for n := 2; n < 1000; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if routeNameFree(servers, candidate) {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%s", base, "extra")
}

func routeNameFree(servers []Server, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, server := range servers {
		if serverMatchesTarget(server, name) {
			return false
		}
	}
	return true
}

func sanitizeRouteName(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(raw) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// pruneOfflineGatewayRoutes splits routes belonging to gateway into kept and
// pruned lists. Routes pointing to other gateways are always kept. Only routes
// on gateway whose (cluster, instance) is absent from live are placed in pruned.
func pruneOfflineGatewayRoutes(servers []Server, gateway string, live []GatewayTarget) ([]Server, []Server) {
	gwKey := gatewayAuthKey(gateway)
	liveSet := make(map[string]struct{}, len(live))
	for _, target := range usableLiveTargets(live) {
		liveSet[strings.TrimSpace(target.Cluster)+"\x00"+strings.TrimSpace(target.Instance)] = struct{}{}
	}
	kept := make([]Server, 0, len(servers))
	var pruned []Server
	for _, server := range servers {
		if gatewayAuthKey(server.Gateway) != gwKey {
			kept = append(kept, server)
			continue
		}
		key := strings.TrimSpace(server.Cluster) + "\x00" + strings.TrimSpace(server.Instance)
		if _, ok := liveSet[key]; ok {
			kept = append(kept, server)
			continue
		}
		pruned = append(pruned, server)
	}
	return kept, pruned
}

func runSync(args []string, servers []Server, configErr error, stdout io.Writer) error {
	return runSyncWithFetcher(args, servers, configErr, stdout, ResolveToken, fetchGatewayTargets, saveServers)
}

func runSyncWithFetcher(
	args []string,
	servers []Server,
	configErr error,
	stdout io.Writer,
	resolveToken func(Server) (string, error),
	fetchTargets func(gateway, token string) ([]GatewayTarget, error),
	persist func([]Server) error,
) error {
	var target string
	var prune, dryRun bool
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.StringVar(&target, "target", "", "Configured target that selects the Gateway login; omit to sync every configured Gateway")
	fs.BoolVar(&prune, "prune", false, "Remove local routes on the synced Gateway(s) that are not currently online/visible")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview routes that would be added or pruned without writing config")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if configErr != nil {
		if os.IsNotExist(configErr) {
			return fmt.Errorf("no local routes yet; add one Gateway target before syncing")
		}
		return configErr
	}
	seeds, err := syncGatewaySeeds(servers, target)
	if err != nil {
		return err
	}
	updated := append([]Server(nil), servers...)
	var added []Server
	var pruned []Server
	var failures []string
	for _, seed := range seeds {
		token, err := resolveToken(seed)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		live, err := fetchTargets(seed.Gateway, token)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		var batch []Server
		updated, batch = mergeLiveGatewayRoutes(updated, seed.Gateway, live)
		added = append(added, batch...)
		afterPrune, stale := pruneOfflineGatewayRoutes(updated, seed.Gateway, live)
		if prune {
			updated = afterPrune
			pruned = append(pruned, stale...)
			suffix := ""
			if dryRun {
				suffix = " (dry-run)"
			}
			fmt.Fprintf(stdout, "gateway %s: %d live, %d new, %d pruned local routes%s\n",
				gatewayAuthKey(seed.Gateway), len(usableLiveTargets(live)), len(batch), len(stale), suffix)
			continue
		}
		fmt.Fprintf(stdout, "gateway %s: %d live, %d new local routes\n", gatewayAuthKey(seed.Gateway), len(usableLiveTargets(live)), len(batch))
		if len(stale) > 0 {
			names := make([]string, 0, len(stale))
			for _, route := range stale {
				names = append(names, route.Name)
			}
			fmt.Fprintf(stdout, "Note: %d local route(s) on %s are not currently online (%s); run \"doops sync --prune --dry-run\" to preview cleanup.\n",
				len(stale), gatewayAuthKey(seed.Gateway), strings.Join(names, ", "))
		}
	}
	if dryRun {
		for _, server := range added {
			fmt.Fprintf(stdout, "Would add local route '%s' (%s/%s).\n", server.Name, server.Cluster, server.Instance)
		}
		for _, server := range pruned {
			fmt.Fprintf(stdout, "Would prune local route '%s' (%s/%s).\n", server.Name, server.Cluster, server.Instance)
		}
		if len(added) == 0 && len(pruned) == 0 && len(failures) == 0 {
			fmt.Fprintln(stdout, "No local route changes (dry-run).")
		}
	} else if len(added) > 0 || len(pruned) > 0 {
		if err := persist(updated); err != nil {
			return err
		}
		for _, server := range added {
			fmt.Fprintf(stdout, "Added local route '%s' (%s/%s).\n", server.Name, server.Cluster, server.Instance)
		}
		for _, server := range pruned {
			fmt.Fprintf(stdout, "Pruned local route '%s' (%s/%s).\n", server.Name, server.Cluster, server.Instance)
		}
	} else if len(failures) == 0 {
		if prune {
			fmt.Fprintln(stdout, "No local routes added or pruned. Local routes match visible live targets.")
		} else {
			fmt.Fprintln(stdout, "No new local routes. Every live target visible to this login is already configured.")
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func usableLiveTargets(live []GatewayTarget) []GatewayTarget {
	usable := make([]GatewayTarget, 0, len(live))
	for _, target := range live {
		cluster := strings.TrimSpace(target.Cluster)
		instance := strings.TrimSpace(target.Instance)
		if cluster == "" || instance == "" || cluster == "*" || instance == "*" {
			continue
		}
		usable = append(usable, target)
	}
	return usable
}

func syncGatewaySeeds(servers []Server, target string) ([]Server, error) {
	target = strings.TrimSpace(target)
	if target != "" {
		server := findServer(servers, target)
		if server == nil {
			return nil, errTargetNotFound(servers, target)
		}
		return []Server{*server}, nil
	}
	seen := map[string]bool{}
	seeds := make([]Server, 0, 1)
	for _, server := range servers {
		key := gatewayAuthKey(server.Gateway)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		seeds = append(seeds, server)
	}
	if len(seeds) == 0 {
		return nil, fmt.Errorf("no Gateway routes configured")
	}
	return seeds, nil
}

// persistVisibleGatewayRoutes stores local routes for Gateway targets the
// current login can see but that are not configured yet. It is used by the
// first login, where no local route exists to select the Gateway from.
func persistVisibleGatewayRoutes(servers []Server, gateway string, live []GatewayTarget) ([]Server, error) {
	_, added := mergeLiveGatewayRoutes(servers, gateway, live)
	if len(added) == 0 {
		return servers, nil
	}
	return persistNewRoutes(added)
}

// persistNewRoutes merges already-authorized routes into the on-disk config
// without rewriting existing names, aliases or descriptions.
func persistNewRoutes(added []Server) ([]Server, error) {
	current, _, err := LoadConfig()
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		current = nil
	}
	updated := current
	for _, route := range added {
		if findSameRoute(updated, route.Gateway, route.Cluster, route.Instance) != "" {
			continue
		}
		var batch []Server
		updated, batch = mergeLiveGatewayRoutes(updated, route.Gateway, []GatewayTarget{{
			Cluster: route.Cluster, Instance: route.Instance,
		}})
		if len(batch) == 0 {
			continue
		}
	}
	if len(updated) == len(current) {
		return current, nil
	}
	if err := saveServers(updated); err != nil {
		return nil, err
	}
	return updated, nil
}
