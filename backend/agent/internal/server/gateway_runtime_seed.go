package server

import (
	"context"
	"fmt"
	"log"
)

// gatewayRuntimeSeedStep is one named piece of startup seeding. Every step must
// be safe to repeat on every boot: a restart re-runs the whole list.
type gatewayRuntimeSeedStep struct {
	name string
	run  func(ctx context.Context, s *GatewayStore) error
}

// gatewayRuntimeSeedSteps is the complete list of rows the Gateway seeds before
// it serves traffic, in execution order. SQLite (migrate) and MySQL
// (gatewayMySQLPrepareRuntimeSchema) both run this list through
// seedRuntimeDefaults, so a seed added here reaches both backends.
//
// INT-33: the MySQL install path used to build the schema and nothing else,
// while SQLite seeded the migration ledger, the edge route revision, the
// built-in system skills and the built-in MCP servers. A MySQL Gateway then
// panicked on the missing edge_route_revision row and served an empty skill and
// MCP catalogue. Adding a step here without wiring it into seedRuntimeDefaults
// cannot happen: the step list is the only source, and
// TestGatewayRuntimeSeedStepsMatchContract pins its membership.
func gatewayRuntimeSeedSteps() []gatewayRuntimeSeedStep {
	return []gatewayRuntimeSeedStep{
		{
			name: "bootstrap-rows",
			run: func(ctx context.Context, s *GatewayStore) error {
				return applyGatewayStoreBootstrapRows(s.db, s.storeDialect())
			},
		},
		{
			name: "builtin-skills",
			run: func(ctx context.Context, s *GatewayStore) error {
				return s.SeedBuiltinSystemSkills(ctx)
			},
		},
		{
			name: "builtin-mcp-servers",
			run: func(ctx context.Context, s *GatewayStore) error {
				return s.SeedBuiltinSystemMCPServers(ctx)
			},
		},
		{
			name: "audit-indexes",
			run: func(ctx context.Context, s *GatewayStore) error {
				if s.storeDialect() == gatewayDialectMySQL {
					// optimizeAuditIndexes is SQLite-only by contract (INT-32): it
					// parses PRAGMA query plans and fails closed on MySQL. The MySQL
					// baseline schema already ships the audit indexes, so there is no
					// work to do here. The skip is explicit, not silent, so an operator
					// reading the log knows the step ran and why it did nothing.
					log.Printf("gateway store: backend=mysql audit index maintenance skipped (SQLite-only step; MySQL baseline ships the indexes)")
					return nil
				}
				_, err := s.optimizeAuditIndexes(ctx)
				return err
			},
		},
	}
}

// seedRuntimeDefaults runs every startup seed for this store's backend. The
// step name is part of the error so a failed boot names the seed, not just a
// table.
func (s *GatewayStore) seedRuntimeDefaults(ctx context.Context) error {
	for _, step := range gatewayRuntimeSeedSteps() {
		if step.run == nil {
			return fmt.Errorf("gateway_runtime_seed_missing: %s", step.name)
		}
		if err := step.run(ctx, s); err != nil {
			return fmt.Errorf("gateway_runtime_seed_failed: %s: %w", step.name, err)
		}
	}
	return nil
}

// storeDialect reports the dialect this store renders SQL for. A store built
// without an explicit dialect is a SQLite store, which is the process default.
func (s *GatewayStore) storeDialect() gatewaySQLDialect {
	if s.dialect == "" {
		return gatewayDialectSQLite
	}
	return s.dialect
}
