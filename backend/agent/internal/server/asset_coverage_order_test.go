package server

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

// assetCoverageOrderFixture is the shared dual-engine fixture for the baseline
// ordering proof. It is written the way the freeze path writes: id is the
// operator-chosen baseline identifier and frozen_at comes from formatTime, so
// the values are RFC3339Nano text. The six rows exercise every part of
// assetCoveragePlatformBaselinesSQL: a later month must come first, two freezes
// in the same second compare by their fractional part (RFC3339Nano trims trailing
// zeros, so ".5Z" and ".25Z" have different widths), and two baselines frozen at
// exactly the same instant can only be separated by the id tie-break.
var assetCoverageOrderFixture = []struct {
	id       string
	frozenAt string
}{
	{"BL-2026-Q0", "2025-12-01T00:00:00.25Z"},
	{"BL-2026-Q1", "2026-01-05T10:00:00.25Z"},
	{"BL-2026-Q2", "2026-01-05T10:00:00.5Z"},
	{"BL-2026-Q3", "2026-02-01T00:00:00Z"},
	{"BL-2026-Q4a", "2026-03-01T00:00:00Z"},
	{"BL-2026-Q4b", "2026-03-01T00:00:00Z"},
}

// assetCoverageOrderWant is the newest-freeze-first order both engines must
// return: the March tie broken by id descending, then February, then the two
// January freezes by fractional part, then December.
var assetCoverageOrderWant = []string{"BL-2026-Q4b", "BL-2026-Q4a", "BL-2026-Q3", "BL-2026-Q2", "BL-2026-Q1", "BL-2026-Q0"}

// TestAssetCoverageBaselineOrderAcrossEngines runs assetCoveragePlatformBaselinesSQL
// -- the statement the coverage handler itself executes -- against SQLite and a
// disposable MySQL 8.4 database holding the same rows. It asserts that the id
// sequences are identical, that the full coverage report lists the baselines in
// that same order, and that the default baseline a caller gets when it names no
// baseline_id is the first row of that order, so "newest freeze wins" cannot
// drift with the storage engine.
func TestAssetCoverageBaselineOrderAcrossEngines(t *testing.T) {
	// SQLite side: the production store, the production migrations and the real
	// HTTP handler.
	f := newCoverageFixture(t)
	for _, row := range assetCoverageOrderFixture {
		f.exec(t, "INSERT INTO asset_coverage_baselines(id,frozen_at,frozen_by,snapshot) VALUES(?,?,?,?)", row.id, row.frozenAt, "qa", "[]")
	}
	sqliteIDs := gatewayCoverageBaselineIDs(t, f.store.db, assetCoveragePlatformBaselinesSQL)
	if !reflect.DeepEqual(sqliteIDs, assetCoverageOrderWant) {
		t.Fatalf("SQLite returned baseline order %v, want %v", sqliteIDs, assetCoverageOrderWant)
	}
	report := coverageDecoded(t, coverageHTTP(t, f, f.admin, "GET", "", "", 200))
	reportIDs := make([]string, 0, len(report.Baselines))
	for _, baseline := range report.Baselines {
		reportIDs = append(reportIDs, baseline.ID)
	}
	if !reflect.DeepEqual(reportIDs, assetCoverageOrderWant) {
		t.Fatalf("SQLite coverage report listed baselines %v, want %v", reportIDs, assetCoverageOrderWant)
	}
	summary := coverageDecoded(t, coverageHTTP(t, f, f.admin, "GET", "?summary=1", "", 200))
	if summary.Baseline == nil || summary.Baseline.ID != assetCoverageOrderWant[0] {
		t.Fatalf("SQLite default baseline = %v, want %s", summary.Baseline, assetCoverageOrderWant[0])
	}

	// MySQL side: the authoritative MySQL DDL in a disposable database, filled
	// with the same rows and asked the same question.
	db := gatewayCopyTestDestination(t)
	if _, err := installGatewayMySQLSchema(context.Background(), db); err != nil {
		t.Fatalf("install MySQL schema: %v", err)
	}
	for _, row := range assetCoverageOrderFixture {
		if _, err := db.Exec("INSERT INTO asset_coverage_baselines(id,frozen_at,frozen_by,snapshot) VALUES(?,?,?,?)", row.id, row.frozenAt, "qa", "[]"); err != nil {
			t.Fatalf("insert baseline %s: %v", row.id, err)
		}
	}
	mysqlIDs := gatewayCoverageBaselineIDs(t, db, assetCoveragePlatformBaselinesSQL)
	if !reflect.DeepEqual(mysqlIDs, assetCoverageOrderWant) {
		t.Fatalf("MySQL returned baseline order %v, want %v", mysqlIDs, assetCoverageOrderWant)
	}

	// The equivalence claim itself, stated as the two sequences a reviewer can
	// compare: identical order, so the same default row.
	if strings.Join(sqliteIDs, ",") != strings.Join(mysqlIDs, ",") {
		t.Fatalf("baseline order differs by engine:\n sqlite: %v\n  mysql: %v", sqliteIDs, mysqlIDs)
	}
	t.Logf("SQLite baseline ids: %v", sqliteIDs)
	t.Logf("MySQL  baseline ids: %v", mysqlIDs)
	t.Logf("default baseline on both engines: %s", mysqlIDs[0])
}

// gatewayCoverageBaselineIDs runs one baseline query and returns the id column in
// row order, so a comparison sees the engine's answer instead of a re-sort.
func gatewayCoverageBaselineIDs(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query baselines: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id, frozenAt string
		if err := rows.Scan(&id, &frozenAt); err != nil {
			t.Fatalf("scan baseline: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate baselines: %v", err)
	}
	return ids
}
