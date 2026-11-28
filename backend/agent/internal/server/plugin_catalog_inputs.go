package server

// Generic, declarative catalog-input resolver registry for Skill-First L2+
// plugins (PLUGIN_ARCHITECTURE_SPEC.md / CAPABILITY_PLUGIN_INTEGRATION_SPEC.md).
//
// Core catalog_inputs expose the 4 built-in Core asset catalogs
// (website_urls, website_domains, database_refs, image_refs).
// Any plugin (installed or builtin) can ALSO declare its own domain-specific
// asset collections via `catalog_schemas[]` in plugin.yaml, stored in
// inventory_resource_catalog under kind="plugin:<slug>:<key>" and automatically
// injected into `DOOPS_PLUGIN_*` env vars — with zero hardcoded domain logic
// in DoOps Core.

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

type catalogInputSpec struct {
	Key         string
	EnvVar      string
	EvidenceKey string
	MaxItems    int
	SourceKind  string
	Extract     func(details map[string]string) (string, bool)
}

var catalogInputRegistry = map[string]catalogInputSpec{
	"website_urls": {
		Key:         "website_urls",
		EnvVar:      "DOOPS_INSPECT_URLS",
		EvidenceKey: "inspect_urls_count",
		MaxItems:    12,
		SourceKind:  "website",
		Extract:     extractCatalogWebsiteURL,
	},
	"website_domains": {
		Key:         "website_domains",
		EnvVar:      "DOOPS_INSPECT_DOMAINS",
		EvidenceKey: "inspect_domains_count",
		MaxItems:    24,
		SourceKind:  "website",
		Extract:     extractCatalogWebsiteDomain,
	},
	"database_refs": {
		Key:         "database_refs",
		EnvVar:      "DOOPS_CATALOG_DATABASES",
		EvidenceKey: "catalog_databases_count",
		MaxItems:    16,
		SourceKind:  "database",
		Extract:     extractCatalogDatabaseRef,
	},
	"image_refs": {
		Key:         "image_refs",
		EnvVar:      "DOOPS_CATALOG_IMAGES",
		EvidenceKey: "catalog_images_count",
		MaxItems:    16,
		SourceKind:  "image",
		Extract:     extractCatalogImageRef,
	},
}

// legacySkillCatalogInputs bridges pre-L2 builtin script skills that do not
// ship a plugin.yaml manifest. All L2+ plugins declare capabilities.catalog_inputs
// or catalog_schemas directly in plugin.yaml.
var legacySkillCatalogInputs = map[string][]string{
	"inspect-app-web": {"website_urls"},
}

func isAllowedCatalogInput(key string) bool {
	_, ok := catalogInputRegistry[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

func allowedCatalogInputKeys() []string {
	keys := make([]string, 0, len(catalogInputRegistry))
	for k := range catalogInputRegistry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func extractCatalogWebsiteURL(d map[string]string) (string, bool) {
	scheme := strings.TrimSpace(d["scheme"])
	domain := strings.TrimSpace(d["domain"])
	p := strings.TrimSpace(d["path"])
	if (scheme != "http" && scheme != "https") || !catalogHost(domain) || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return "", false
	}
	if strings.ContainsAny(p, ", \t\r\n?#@\\") {
		return "", false
	}
	candidate := scheme + "://" + domain + p
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	return candidate, true
}

func extractCatalogWebsiteDomain(d map[string]string) (string, bool) {
	domain := strings.ToLower(strings.TrimSpace(d["domain"]))
	if !catalogHost(domain) || strings.ContainsAny(domain, ", \t\r\n?#@\\/") {
		return "", false
	}
	return domain, true
}

func extractCatalogDatabaseRef(d map[string]string) (string, bool) {
	engine := strings.ToLower(strings.TrimSpace(d["engine"]))
	host := strings.ToLower(strings.TrimSpace(d["host"]))
	port := strings.TrimSpace(d["port"])
	dbName := strings.TrimSpace(d["database"])
	if (engine != "postgresql" && engine != "mysql" && engine != "mongodb") ||
		!catalogHost(host) || !catalogPort(port) || !assetLabel.MatchString(dbName) {
		return "", false
	}
	ref := engine + "://" + host + ":" + port + "/" + dbName
	if strings.ContainsAny(ref, ", \t\r\n?#@\\") {
		return "", false
	}
	return ref, true
}

func extractCatalogImageRef(d map[string]string) (string, bool) {
	registry := strings.ToLower(strings.TrimSpace(d["registry"]))
	repo := strings.TrimSpace(d["repository"])
	ver := strings.TrimSpace(d["version"])
	if registry == "" || !repoPart.MatchString(repo) || !imageVersion.MatchString(ver) {
		return "", false
	}
	sep := ":"
	if strings.HasPrefix(ver, "sha256:") {
		sep = "@"
	}
	ref := registry + "/" + repo + sep + ver
	if strings.ContainsAny(ref, ", \t\r\n?#\\") {
		return "", false
	}
	return ref, true
}

// declaredCatalogInputsForSkill returns the deduplicated list of Core catalog
// input keys declared by a platform-installed or builtin system Skill. Custom
// personal skills never qualify for automatic fleet Core catalog injection.
func declaredCatalogInputsForSkill(sk platformSkillRecord) []string {
	if sk.Scope != "system" {
		return nil
	}
	var requested []string
	if manifest := decodeStoredSkillUIManifest(sk.UIManifestJSON); manifest != nil && len(manifest.Capabilities.CatalogInputs) > 0 {
		requested = manifest.Capabilities.CatalogInputs
	} else if legacy, ok := legacySkillCatalogInputs[sk.Slug]; ok {
		requested = legacy
	}
	if len(requested) == 0 {
		return nil
	}
	out := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, key := range requested {
		k := strings.ToLower(strings.TrimSpace(key))
		if _, ok := catalogInputRegistry[k]; !ok || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// resolveCatalogInputsForTarget performs a single batched SQL query across the
// target's inventory_resource_catalog rows for all requested Core catalog keys.
func (h *GatewayHub) resolveCatalogInputsForTarget(
	ctx context.Context,
	cluster, instance string,
	inputKeys []string,
) (map[string]string, map[string]int) {
	if h == nil || h.store == nil || len(inputKeys) == 0 || !h.store.tableExists(ctx, "inventory_resource_catalog") {
		return nil, nil
	}
	specs := make([]catalogInputSpec, 0, len(inputKeys))
	kindSet := make(map[string]bool, len(inputKeys))
	kinds := make([]string, 0, len(inputKeys))
	for _, rawKey := range inputKeys {
		spec, ok := catalogInputRegistry[strings.ToLower(strings.TrimSpace(rawKey))]
		if !ok {
			continue
		}
		specs = append(specs, spec)
		if !kindSet[spec.SourceKind] {
			kindSet[spec.SourceKind] = true
			kinds = append(kinds, spec.SourceKind)
		}
	}
	if len(specs) == 0 || len(kinds) == 0 {
		return nil, nil
	}
	return h.queryCatalogSpecsForTarget(ctx, cluster, instance, kinds, specs)
}

func (h *GatewayHub) resolvePluginCatalogSchemaInputs(
	ctx context.Context,
	sk platformSkillRecord,
	target monitorTargetRef,
) (map[string]string, map[string]int) {
	if h == nil || h.store == nil || sk.Scope != "system" || !h.store.tableExists(ctx, "inventory_resource_catalog") {
		return nil, nil
	}
	manifest := decodeStoredSkillUIManifest(sk.UIManifestJSON)
	if manifest == nil || len(manifest.CatalogSchemas) == 0 {
		return nil, nil
	}
	specs := make([]catalogInputSpec, 0, len(manifest.CatalogSchemas))
	kinds := make([]string, 0, len(manifest.CatalogSchemas))
	for _, sc := range manifest.CatalogSchemas {
		var primaryField SkillPluginCatalogField
		found := false
		for _, f := range sc.Fields {
			if f.Key == sc.PrimaryField {
				primaryField = f
				found = true
				break
			}
		}
		if !found {
			continue
		}
		kind := "plugin:" + sk.Slug + ":" + sc.Key
		maxItems := sc.MaxItems
		if maxItems <= 0 {
			maxItems = 24
		}
		pf := primaryField
		kinds = append(kinds, kind)
		specs = append(specs, catalogInputSpec{
			Key:         sc.Key,
			EnvVar:      sc.EnvVar,
			EvidenceKey: "plugin_catalog_" + sc.Key + "_count",
			MaxItems:    maxItems,
			SourceKind:  kind,
			Extract: func(d map[string]string) (string, bool) {
				v, exists := d[pf.Key]
				if !exists {
					return "", false
				}
				norm, ok := validatePluginCatalogFieldValue(pf, v)
				if !ok || strings.ContainsAny(norm, ", \t\r\n?#@\\") {
					return "", false
				}
				return norm, true
			},
		})
	}
	if len(specs) == 0 {
		return nil, nil
	}
	return h.queryCatalogSpecsForTarget(ctx, target.Cluster, target.Instance, kinds, specs)
}

func (h *GatewayHub) queryCatalogSpecsForTarget(
	ctx context.Context,
	cluster, instance string,
	kinds []string,
	specs []catalogInputSpec,
) (map[string]string, map[string]int) {
	sort.Strings(kinds)
	placeholders := strings.TrimRight(strings.Repeat("?,", len(kinds)), ",")
	args := make([]any, 0, len(kinds)+2)
	args = append(args, cluster, instance)
	for _, k := range kinds {
		args = append(args, k)
	}
	rows, err := h.store.db.QueryContext(ctx,
		`SELECT kind, details FROM inventory_resource_catalog WHERE cluster=? AND instance=? AND kind IN (`+placeholders+`) ORDER BY kind, id LIMIT 160`,
		args...)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	detailsByKind := make(map[string][]map[string]string, len(kinds))
	for rows.Next() {
		var kind string
		var rawDetails []byte
		if rows.Scan(&kind, &rawDetails) != nil {
			continue
		}
		var d map[string]string
		if json.Unmarshal(rawDetails, &d) != nil {
			continue
		}
		detailsByKind[kind] = append(detailsByKind[kind], d)
	}

	env := make(map[string]string, len(specs))
	counts := make(map[string]int, len(specs))
	for _, spec := range specs {
		rowsForKind := detailsByKind[spec.SourceKind]
		if len(rowsForKind) == 0 {
			continue
		}
		vals := make([]string, 0, spec.MaxItems)
		seen := make(map[string]bool, spec.MaxItems)
		for _, d := range rowsForKind {
			val, ok := spec.Extract(d)
			if !ok || seen[val] {
				continue
			}
			seen[val] = true
			vals = append(vals, val)
			if len(vals) >= spec.MaxItems {
				break
			}
		}
		if len(vals) > 0 {
			env[spec.EnvVar] = strings.Join(vals, ",")
			counts[spec.EvidenceKey] = len(vals)
		}
	}
	if len(env) == 0 {
		return nil, nil
	}
	return env, counts
}

// resolveSkillCatalogInputs resolves both Core catalog_inputs and plugin-owned
// catalog_schemas for a Skill execution on a target. Zero per-skill slug switches.
func (h *GatewayHub) resolveSkillCatalogInputs(
	ctx context.Context,
	sk platformSkillRecord,
	target monitorTargetRef,
) (map[string]string, map[string]int) {
	mergedEnv := make(map[string]string)
	mergedCounts := make(map[string]int)

	if keys := declaredCatalogInputsForSkill(sk); len(keys) > 0 {
		env, counts := h.resolveCatalogInputsForTarget(ctx, target.Cluster, target.Instance, keys)
		for k, v := range env {
			mergedEnv[k] = v
		}
		for k, v := range counts {
			mergedCounts[k] = v
		}
	}
	if pEnv, pCounts := h.resolvePluginCatalogSchemaInputs(ctx, sk, target); len(pEnv) > 0 {
		for k, v := range pEnv {
			mergedEnv[k] = v
		}
		for k, v := range pCounts {
			mergedCounts[k] = v
		}
	}
	if len(mergedEnv) == 0 {
		return nil, nil
	}
	return mergedEnv, mergedCounts
}
