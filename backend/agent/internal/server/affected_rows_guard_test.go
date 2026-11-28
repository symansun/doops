package server

// INT-65: static guard for RowsAffected() semantics.
//
// MySQL's affected-rows count is "rows changed", SQLite's is "rows matched", so
// an `RowsAffected() == 0` check that means "the row is gone" is a
// dialect-dependent bug: it reports a present row as missing whenever the
// UPDATE wrote the value the row already had (INT-61 audit,
// docs/reviews/2026-11-24-mysql-affected-rows-audit.md section 3.1). The agreed
// fix pattern is an existence SELECT on the same connection/transaction when
// the UPDATE reports 0.
//
// affected_rows_manifest.json freezes every call site in this package and its
// category, so that
//
//	(1) a new RowsAffected() call cannot land without a recorded decision,
//	(2) a site whose SET value can repeat cannot land without the
//	    existence-select mitigation,
//	(3) a manifest entry cannot outlive the code it describes.
//
// The scan is pure go/ast: it needs no database and runs in ordinary CI.

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// affectedRowsManifestFile is the frozen inventory, relative to the package
// directory (the test working directory).
const affectedRowsManifestFile = "affected_rows_manifest.json"

// affectedRowsCallSite is one `<expr>.RowsAffected()` call located in a
// function. File+Func+Occurrence is the stable identity: line numbers drift
// whenever the function is edited, the occurrence inside the function does not.
type affectedRowsCallSite struct {
	File       string
	Func       string
	Occurrence int
	Line       int
}

func (s affectedRowsCallSite) key() string {
	return fmt.Sprintf("%s:%s#%d", s.File, s.Func, s.Occurrence)
}

// affectedRowsManifestEntry is one record of affected_rows_manifest.json. The
// booleans are pointers so a missing field is an error instead of a silent
// false.
type affectedRowsManifestEntry struct {
	File              string `json:"file"`
	Func              string `json:"func"`
	Occurrence        int    `json:"occurrence"`
	Category          string `json:"category"`
	SameValuePossible *bool  `json:"same_value_possible"`
	MySQLReachable    *bool  `json:"mysql_reachable"`
	Mitigation        string `json:"mitigation"`
	Note              string `json:"note"`
}

func (e affectedRowsManifestEntry) key() string {
	return fmt.Sprintf("%s:%s#%d", e.File, e.Func, e.Occurrence)
}

// scanAffectedRowsSites parses every non-test .go file in the package and
// returns the RowsAffected() call sites, in file then source order. Calls
// inside closures are attributed to the enclosing named function.
func scanAffectedRowsSites(t *testing.T) []affectedRowsCallSite {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var sites []affectedRowsCallSite
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			occurrence := 0
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "RowsAffected" || len(call.Args) != 0 {
					return true
				}
				occurrence++
				sites = append(sites, affectedRowsCallSite{
					File:       name,
					Func:       fn.Name.Name,
					Occurrence: occurrence,
					Line:       fset.Position(call.Pos()).Line,
				})
				return true
			})
		}
	}
	sort.SliceStable(sites, func(i, j int) bool {
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		return sites[i].Line < sites[j].Line
	})
	return sites
}

// loadAffectedRowsManifest reads and fully decodes the manifest. Unknown fields
// are errors so a renamed key cannot silently drop a guarantee.
func loadAffectedRowsManifest(t *testing.T) []affectedRowsManifestEntry {
	t.Helper()
	data, err := os.ReadFile(affectedRowsManifestFile)
	if err != nil {
		t.Fatalf("read %s: %v (the guard needs the frozen inventory next to the package)", affectedRowsManifestFile, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var entries []affectedRowsManifestEntry
	if err := decoder.Decode(&entries); err != nil {
		t.Fatalf("decode %s: %v", affectedRowsManifestFile, err)
	}
	if decoder.More() {
		t.Fatalf("%s has trailing content after the JSON array", affectedRowsManifestFile)
	}
	return entries
}

func affectedRowsManifestIndex(t *testing.T, entries []affectedRowsManifestEntry) map[string]affectedRowsManifestEntry {
	t.Helper()
	index := make(map[string]affectedRowsManifestEntry, len(entries))
	for _, entry := range entries {
		key := entry.key()
		if _, duplicate := index[key]; duplicate {
			t.Errorf("duplicate manifest entry %s: one site, one decision", key)
		}
		index[key] = entry
	}
	return index
}

// TestAffectedRowsManifestMatchesCode is assertion (1) and (3): every call in
// the package must be registered, and every registered call must still exist.
// A new `res.RowsAffected()` without a decision fails with
// file:func#occurrence, which is enough to find it without a line number.
func TestAffectedRowsManifestMatchesCode(t *testing.T) {
	sites := scanAffectedRowsSites(t)
	if len(sites) == 0 {
		t.Fatalf("scan found no RowsAffected() call in this package; the scanner or the package layout changed, so the guard would pass vacuously")
	}
	entries := loadAffectedRowsManifest(t)
	manifest := affectedRowsManifestIndex(t, entries)
	code := make(map[string]struct{}, len(sites))
	for _, site := range sites {
		code[site.key()] = struct{}{}
		if _, registered := manifest[site.key()]; !registered {
			t.Errorf("unregistered RowsAffected() call at %s (line %d): add %q to %s with category, same_value_possible, mysql_reachable and mitigation",
				site.key(), site.Line, site.key(), affectedRowsManifestFile)
		}
	}
	for _, entry := range entries {
		if _, present := code[entry.key()]; !present {
			t.Errorf("stale manifest entry %s: no RowsAffected() call with that file/function/occurrence exists, so the inventory has rotted; delete or fix the entry", entry.key())
		}
	}
	counts := map[string]int{}
	for _, entry := range entries {
		counts[entry.Category]++
	}
	t.Logf("affected-rows guard: %d RowsAffected() sites in %d files; manifest %d entries (exists=%d changed=%d count=%d helper=%d)",
		len(sites), len(codeFiles(sites)), len(entries), counts["exists"], counts["changed"], counts["count"], counts["helper"])
}

func codeFiles(sites []affectedRowsCallSite) map[string]struct{} {
	files := make(map[string]struct{}, len(sites))
	for _, site := range sites {
		files[site.File] = struct{}{}
	}
	return files
}

// TestAffectedRowsManifestEntriesAreWellFormed keeps the manifest itself
// decidable: an entry with an unknown category, a missing boolean or an empty
// note is a hole in the inventory, not a pass.
func TestAffectedRowsManifestEntriesAreWellFormed(t *testing.T) {
	entries := loadAffectedRowsManifest(t)
	if len(entries) == 0 {
		t.Fatalf("%s is empty", affectedRowsManifestFile)
	}
	validCategories := map[string]bool{"exists": true, "changed": true, "count": true, "helper": true}
	validMitigations := map[string]bool{"existence-select": true, "not-needed": true, "helper-callers": true}
	for _, entry := range entries {
		if entry.File == "" || entry.Func == "" || entry.Occurrence < 1 {
			t.Errorf("manifest entry %s needs a file, a function and occurrence >= 1", entry.key())
		}
		if !validCategories[entry.Category] {
			t.Errorf("manifest entry %s has category %q, want one of exists/changed/count/helper", entry.key(), entry.Category)
		}
		if !validMitigations[entry.Mitigation] {
			t.Errorf("manifest entry %s has mitigation %q, want one of existence-select/not-needed/helper-callers", entry.key(), entry.Mitigation)
		}
		if entry.SameValuePossible == nil {
			t.Errorf("manifest entry %s must state same_value_possible explicitly", entry.key())
		}
		if entry.MySQLReachable == nil {
			t.Errorf("manifest entry %s must state mysql_reachable explicitly", entry.key())
		}
		if strings.TrimSpace(entry.Note) == "" {
			t.Errorf("manifest entry %s needs a note explaining how 0 is interpreted", entry.key())
		}
		if entry.Mitigation == "existence-select" && entry.SameValuePossible != nil && !*entry.SameValuePossible {
			t.Errorf("manifest entry %s claims the existence-select mitigation but same_value_possible=false; the mitigation would be dead code", entry.key())
		}
	}
}

// TestAffectedRowsSameValueSitesCarryExistenceSelect is assertion (2): a site
// whose UPDATE can write the value the row already has must not interpret
// affected==0 as absence, so it must carry the existence-select mitigation.
// The five keys pinned below are the INT-61/INT-65 fixes; pinning them by name
// stops a manifest edit from quietly turning the boolean off.
func TestAffectedRowsSameValueSitesCarryExistenceSelect(t *testing.T) {
	entries := loadAffectedRowsManifest(t)
	manifest := affectedRowsManifestIndex(t, entries)
	for _, entry := range entries {
		if entry.SameValuePossible != nil && *entry.SameValuePossible && entry.Mitigation != "existence-select" {
			t.Errorf("manifest entry %s: same_value_possible=true requires mitigation=existence-select, got %q; a same-value UPDATE reporting 0 would be read as a missing row",
				entry.key(), entry.Mitigation)
		}
	}
	fixed := []string{
		"agent_resource_store.go:saveAgentResourceApplied#1",
		"gateway_store_admin.go:SetUserDisabled#1",
		"gateway_store_agents.go:UnarchiveAgentStatus#1",
		"audit_purge.go:fail#1",
		"audit_purge.go:writeAuditPurgeReceipt#1",
	}
	for _, key := range fixed {
		entry, registered := manifest[key]
		if !registered {
			t.Errorf("fixed same-value site %s is missing from %s", key, affectedRowsManifestFile)
			continue
		}
		if entry.SameValuePossible == nil || !*entry.SameValuePossible {
			t.Errorf("fixed same-value site %s must keep same_value_possible=true", key)
		}
		if entry.Mitigation != "existence-select" {
			t.Errorf("fixed same-value site %s must keep mitigation=existence-select, got %q", key, entry.Mitigation)
		}
	}
}
