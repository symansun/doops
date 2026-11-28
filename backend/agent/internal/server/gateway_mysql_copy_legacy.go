package server

// gatewayCopyLegacyTables and gatewayCopyLegacyColumns are the copier's only
// allowance for a schema that legitimately grew apart from the reviewed DDL.
// A Gateway database written by an older release can still carry objects for
// features that were deleted later; a database created by the current release
// never creates them. Neither side is a drift, so the copier accepts exactly
// these names and reports what it left behind. Anything outside this list is
// still rejected with mysql_copy_schema_mismatch: the list is frozen, matched
// by exact name, and never inferred from a prefix or a pattern.
//
// Adding an entry requires a historical commit that deleted the feature the
// object belonged to. The tables below came from:
//
//	git_repos, release_events, release_tickets, scheduler_issues,
//	scheduler_job_leases, scheduler_jobs
//	    deleted with "refactor(gateway): remove scheduled issue jobs and git
//	    repo registry" (98cd6000 / 0b815aaa); release events and tickets were
//	    part of the same console-era release registry and are unreferenced
//	    since, confirmed by grep over backend/agent.
//	upgrade_operations
//	    the pre-0.3.19 upgrade bookkeeping table; unreferenced since the
//	    Agent upgrade contract moved to the controller-driven path.
//
// The columns came from the same capture: audit_events.project/environment/
// template/namespace were the console-era audit scope fields, and
// users.account_type the pre-0.3.19 account classification. No runtime code
// reads either group, and the frozen export still defines them with defaults.
var gatewayCopyLegacyTables = []string{
	"git_repos",
	"release_events",
	"release_tickets",
	"scheduler_issues",
	"scheduler_job_leases",
	"scheduler_jobs",
	"upgrade_operations",
}

var gatewayCopyLegacyColumns = map[string][]string{
	"audit_events": {"environment", "namespace", "project", "template"},
	"users":        {"account_type"},
}

// gatewayCopyLegacyTable reports whether name is one of the frozen legacy
// tables. The copier only accepts a destination table missing from the source
// when this returns true; a source table missing from the destination is
// never tolerated.
func gatewayCopyLegacyTable(name string) bool {
	for _, legacy := range gatewayCopyLegacyTables {
		if legacy == name {
			return true
		}
	}
	return false
}

// gatewayCopyLegacyColumn reports whether table.column is one of the frozen
// legacy columns. Such a column is never copied: the destination keeps its
// DDL default, and the report records that the value was skipped.
func gatewayCopyLegacyColumn(table, column string) bool {
	for _, legacy := range gatewayCopyLegacyColumns[table] {
		if legacy == column {
			return true
		}
	}
	return false
}
