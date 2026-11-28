CREATE UNIQUE INDEX asset_monitor_one_open ON asset_monitor_alerts(series_id) WHERE state='open';
CREATE INDEX cluster_inventory_asset_id ON cluster_inventory_sources(asset_id);
CREATE INDEX idx_agent_conn_events_target ON agent_connection_events(cluster, instance, occurred_at_ms DESC);
CREATE INDEX idx_agent_conn_events_time ON agent_connection_events(occurred_at_ms DESC, id DESC);
CREATE INDEX idx_audit_action_id ON audit_events(action, id DESC, status);
CREATE INDEX idx_audit_session_id ON audit_events(session, id DESC);
CREATE INDEX idx_audit_session_target_id ON audit_events(session, cluster, instance, id DESC);
CREATE INDEX idx_audit_started_ms ON audit_events(started_at_ms DESC, id DESC);
CREATE INDEX idx_audit_status_id ON audit_events(status, id DESC, action);
CREATE INDEX idx_audit_target_action_id ON audit_events(cluster, instance, action, id DESC, status);
CREATE INDEX idx_audit_target_id ON audit_events(cluster, instance, id DESC);
CREATE INDEX idx_audit_target_status_id ON audit_events(cluster, instance, status, id DESC, action);
CREATE INDEX idx_audit_user_action_id ON audit_events(user_id, action, id DESC, status);
CREATE INDEX idx_audit_user_id ON audit_events(user_id, id DESC);
CREATE INDEX idx_audit_user_session_id ON audit_events(user_id, session, id DESC);
CREATE INDEX idx_audit_user_status_id ON audit_events(user_id, status, id DESC, action);
CREATE INDEX idx_credential_bundle_items_resource
		ON credential_bundle_items(credential_id);
CREATE INDEX idx_credential_grants_authorize
		ON credential_grants(grantee_id, state, credential_id);
CREATE INDEX idx_credential_materializations_resource
				ON credential_materializations(credential_id, version_id, grant_id, status);
CREATE INDEX idx_credential_verifications_resource
		ON credential_verifications(credential_id, version_id, status);
CREATE UNIQUE INDEX idx_credential_versions_one_active
			ON credential_versions(credential_id) WHERE state = 'active';
CREATE INDEX idx_credential_versions_resource ON credential_versions(credential_id, created_at DESC);
CREATE INDEX idx_credentials_name ON credentials(name, state);
CREATE INDEX idx_credentials_owner ON credentials(owner_id, state, created_at DESC);
CREATE INDEX idx_detached_tasks_user ON detached_tasks(user_id, audit_id DESC);
CREATE UNIQUE INDEX idx_edge_routes_hostname ON edge_routes(hostname);
CREATE INDEX idx_execution_records_expiry ON execution_records(expires_at_ms);
CREATE INDEX idx_git_repos_created ON git_repos(created_at);
CREATE INDEX idx_grants_user ON grants(user_id);
CREATE INDEX idx_monitor_run_items_run ON monitor_run_items(run_id, id);
CREATE INDEX idx_monitor_run_items_target ON monitor_run_items(cluster, instance, started_at_ms DESC);
CREATE INDEX idx_monitor_runs_started ON monitor_runs(started_at_ms DESC, id DESC);
CREATE INDEX idx_monitor_schedules_due ON monitor_schedules(enabled, next_run_at_ms);
CREATE INDEX idx_monitor_snapshots_latest ON monitor_resource_snapshots(cluster, instance, asset_kind, asset_id, collected_at_ms DESC);
CREATE INDEX idx_monitor_snapshots_time ON monitor_resource_snapshots(collected_at_ms DESC);
CREATE INDEX idx_platform_mcp_scope ON platform_mcp_servers(scope, enabled);
CREATE INDEX idx_platform_skills_scope ON platform_skills(scope, category, enabled);
CREATE INDEX idx_proxy_routes_target ON proxy_routes(cluster, instance, enabled);
CREATE INDEX idx_release_events_ticket ON release_events(release_number, id);
CREATE INDEX idx_release_tickets_scope_status ON release_tickets(scope, status, number);
CREATE INDEX idx_release_tickets_status ON release_tickets(status, number);
CREATE INDEX idx_sched_issues_dedup ON scheduler_issues(repo_slug, fingerprint, created_at);
CREATE INDEX idx_tokens_kind ON tokens(kind);
CREATE UNIQUE INDEX inspection_cycle ON inspection_runs(plan_id,due,(trigger_kind='manual'));
CREATE INDEX inspection_schedule ON inspection_plans(active,next_due);
CREATE INDEX inspection_work ON inspection_items(status,deadline,id);
CREATE INDEX idx_inspection_metric_points_run ON inspection_metric_points(run_id);
CREATE INDEX idx_inspection_metric_points_series ON inspection_metric_points(skill_slug, metric_key, collected_at_ms);
CREATE INDEX idx_inspection_metric_points_target ON inspection_metric_points(cluster, instance, collected_at_ms);
CREATE INDEX inventory_business_deployment_target
    ON inventory_business_deployments(cluster, instance, asset_id);
CREATE INDEX inventory_import_attempts_batch ON inventory_import_attempts(batch_id, attempt_id);
CREATE INDEX inventory_machines_host ON inventory_machines(host_asset_id) WHERE host_asset_id IS NOT NULL;
CREATE INDEX inventory_machines_target ON inventory_machines(cluster, instance);
CREATE INDEX inventory_node_links_machine ON inventory_node_links(machine_id);
-- The four indexes and the eight tables from agent_resource_state to
-- personal_asset_records below come from the operator migrations under
-- backend/docs/migrations (the production dump predated them);
-- agent_observations uses "key" as a column name, exactly as the migration does.
CREATE UNIQUE INDEX catalog_bound_identity ON inventory_resource_catalog(kind,cluster,instance,identity_key) WHERE cluster<>'';
CREATE UNIQUE INDEX catalog_pending_identity ON inventory_resource_catalog(kind,created_by,identity_key) WHERE cluster='';
CREATE INDEX idx_agent_observations_target ON agent_observations(cluster, instance, kind, key);
CREATE INDEX inventory_machine_tags_by_tag ON inventory_machine_tags(tag, asset_id);
CREATE TABLE agent_connection_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			generation INTEGER NOT NULL DEFAULT 0,
			event TEXT NOT NULL,
			release_commit TEXT NOT NULL DEFAULT '',
			remote TEXT NOT NULL DEFAULT '',
			enrolled INTEGER NOT NULL DEFAULT 0,
			occurred_at_ms INTEGER NOT NULL,
			occurred_at TEXT NOT NULL
		);
CREATE TABLE agent_enrollments (
		id TEXT PRIMARY KEY,
		cluster TEXT NOT NULL,
		instance TEXT NOT NULL,
		prefix TEXT NOT NULL,
		hash TEXT NOT NULL,
		revoked_at TEXT NOT NULL DEFAULT '',
		confirmed_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	);
CREATE TABLE agent_status (
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			token_id TEXT NOT NULL DEFAULT '',
			remote TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'offline',
			connected_at TEXT NOT NULL DEFAULT '',
			last_seen TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL, generation INTEGER NOT NULL DEFAULT 0, archived_at TEXT NOT NULL DEFAULT '', archived_by TEXT NOT NULL DEFAULT '', release_commit TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT '', kubernetes_available INTEGER,
			PRIMARY KEY(cluster, instance)
		);
CREATE TABLE asset_coverage_baselines (
    id TEXT PRIMARY KEY NOT NULL,
    frozen_at TEXT NOT NULL,
    frozen_by TEXT NOT NULL,
    snapshot TEXT NOT NULL CHECK (json_valid(snapshot))
) STRICT;
CREATE TABLE asset_coverage_cluster_versions (
    cluster_id TEXT NOT NULL,
    id TEXT NOT NULL,
    frozen_at TEXT NOT NULL,
    frozen_by TEXT NOT NULL,
    snapshot TEXT NOT NULL CHECK(json_valid(snapshot)),
    document_ref TEXT NOT NULL,
    register_revision INTEGER NOT NULL,
    PRIMARY KEY(cluster_id,id)
) STRICT;
CREATE TABLE asset_coverage_registers (
    scope TEXT PRIMARY KEY NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    document_ref TEXT NOT NULL,
    entries TEXT NOT NULL CHECK(json_valid(entries)),
    updated_by TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
CREATE TABLE asset_monitor_alerts (
 series_id INTEGER NOT NULL REFERENCES asset_monitor_series(id), opened_ms INTEGER NOT NULL,
 threshold REAL NOT NULL, duration_seconds INTEGER NOT NULL, ended_ms INTEGER,
 state TEXT NOT NULL CHECK(state IN ('open','recovered','interrupted','reconfigured')),
 PRIMARY KEY(series_id,opened_ms)
) WITHOUT ROWID;
CREATE TABLE asset_monitor_rules (
 series_id INTEGER PRIMARY KEY REFERENCES asset_monitor_series(id), threshold REAL NOT NULL CHECK(threshold>=0),
 duration_seconds INTEGER NOT NULL CHECK(duration_seconds BETWEEN 1 AND 604800), effective_ms INTEGER NOT NULL,
 first_above_ms INTEGER
);
CREATE TABLE asset_monitor_samples (
 series_id INTEGER NOT NULL REFERENCES asset_monitor_series(id), at_ms INTEGER NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('ok','missing','not_applicable')), value REAL,
 CHECK((status='ok' AND value IS NOT NULL AND value>=0) OR (status<>'ok' AND value IS NULL)),
 PRIMARY KEY(series_id,at_ms)
) WITHOUT ROWID;
CREATE TABLE asset_monitor_series (
 id INTEGER PRIMARY KEY, cluster TEXT NOT NULL, instance TEXT NOT NULL, namespace TEXT NOT NULL,
 type TEXT NOT NULL CHECK(type IN ('machine','os','kernel','process','app','database')),
 asset_id TEXT NOT NULL, origin TEXT NOT NULL CHECK(origin IN ('k8s_top','exporter')),
 metric TEXT NOT NULL CHECK(metric IN ('cpu','memory','gpu','network','requests','storage','storage_total','storage_available')),
 device_id TEXT NOT NULL DEFAULT '',
 CHECK((metric='gpu' AND length(device_id)=40 AND substr(device_id,1,4)='GPU-')
    OR (metric='network' AND length(device_id) BETWEEN 1 AND 15 AND device_id GLOB '[a-zA-Z0-9]*' AND device_id NOT GLOB '*[^a-zA-Z0-9_.-]*')
    OR (metric IN ('storage','storage_total','storage_available') AND substr(device_id,1,1)='/')
    OR (metric IN ('cpu','memory','requests','storage','network') AND device_id='')),
 UNIQUE(cluster,instance,type,asset_id,namespace,origin,metric,device_id)
);
CREATE TABLE asset_runtime_bindings (
 cluster TEXT NOT NULL, instance TEXT NOT NULL, namespace TEXT NOT NULL, application TEXT NOT NULL,
 workload_uid TEXT NOT NULL, verified_at TEXT NOT NULL,
 PRIMARY KEY(cluster,instance,namespace,application,workload_uid)
) STRICT;
CREATE TABLE asset_runtime_snapshots (
 cluster TEXT NOT NULL, instance TEXT NOT NULL, namespace TEXT NOT NULL, asset_id TEXT NOT NULL,
 revision TEXT NOT NULL, observed_at TEXT NOT NULL, objects TEXT NOT NULL CHECK(json_valid(objects)),
 PRIMARY KEY(cluster,instance,namespace)
) STRICT;
CREATE TABLE audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL DEFAULT '',
			token_id TEXT NOT NULL DEFAULT '',
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			session TEXT NOT NULL DEFAULT '',
			command_summary TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'running',
			error TEXT NOT NULL DEFAULT '',
			tail TEXT NOT NULL DEFAULT '',
			bytes_in INTEGER NOT NULL DEFAULT 0,
			bytes_out INTEGER NOT NULL DEFAULT 0,
			started_at TEXT NOT NULL,
			ended_at TEXT NOT NULL DEFAULT ''
		, credential_id TEXT NOT NULL DEFAULT '', credential_version_id TEXT NOT NULL DEFAULT '', credential_grant_id TEXT NOT NULL DEFAULT '', project TEXT NOT NULL DEFAULT '', environment TEXT NOT NULL DEFAULT '', template TEXT NOT NULL DEFAULT '', namespace TEXT NOT NULL DEFAULT '', resource_version TEXT NOT NULL DEFAULT '', digest TEXT NOT NULL DEFAULT '', correlation_id TEXT NOT NULL DEFAULT '', scope_json TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0, started_at_ms INTEGER NOT NULL DEFAULT 0);
CREATE TABLE cluster_inventory_namespaces (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    name TEXT NOT NULL,
    uid TEXT NOT NULL,
    namespace_observed_at TEXT NOT NULL,
    pods_listed_at TEXT NOT NULL,
    verified_at TEXT NOT NULL,
    verified_by TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('list_namespaces', 'list_pods')),
    PRIMARY KEY (cluster, instance, name),
    FOREIGN KEY (cluster, instance) REFERENCES cluster_inventory_sources(cluster, instance)
);
CREATE TABLE cluster_inventory_sources (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    name TEXT NOT NULL,
    kube_system_uid TEXT NOT NULL DEFAULT '',
    verified_at TEXT NOT NULL,
    verified_by TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('list_namespaces', 'list_pods')),
    PRIMARY KEY (cluster, instance)
);
CREATE TABLE "credential_bundle_items" (
		bundle_id TEXT NOT NULL,
		credential_id TEXT NOT NULL,
		PRIMARY KEY(bundle_id, credential_id),
		FOREIGN KEY(bundle_id) REFERENCES credential_bundles(id),
		FOREIGN KEY(credential_id) REFERENCES credentials(id)
	);
CREATE TABLE credential_bundles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'personal',
			owner_id TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'active',
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(owner_id, name),
			FOREIGN KEY(owner_id) REFERENCES users(id),
			FOREIGN KEY(created_by) REFERENCES users(id)
		);
CREATE TABLE credential_grants (
		id TEXT PRIMARY KEY,
		credential_id TEXT NOT NULL,
		grantee_id TEXT NOT NULL,
		scope_json TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'active',
		created_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		revoked_at TEXT NOT NULL DEFAULT '',
		UNIQUE(credential_id, grantee_id, scope_json),
		FOREIGN KEY(credential_id) REFERENCES credentials(id),
		FOREIGN KEY(grantee_id) REFERENCES users(id),
		FOREIGN KEY(created_by) REFERENCES users(id)
	);
CREATE TABLE credential_materializations (
				id TEXT PRIMARY KEY,
				credential_id TEXT NOT NULL,
				version_id TEXT NOT NULL,
				grant_id TEXT NOT NULL,
				use TEXT NOT NULL,
				context_key TEXT NOT NULL,
				request_json TEXT NOT NULL,
				evidence_json TEXT NOT NULL DEFAULT '{}',
				status TEXT NOT NULL,
				error_category TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(version_id, grant_id, use, context_key),
				FOREIGN KEY(credential_id) REFERENCES credentials(id),
				FOREIGN KEY(version_id) REFERENCES credential_versions(id),
				FOREIGN KEY(grant_id) REFERENCES credential_grants(id)
			);
CREATE TABLE "credential_verifications" (
		id TEXT PRIMARY KEY,
		credential_id TEXT NOT NULL,
		version_id TEXT NOT NULL,
		grant_id TEXT NOT NULL,
		use TEXT NOT NULL,
		context_key TEXT NOT NULL,
		request_json TEXT NOT NULL,
		evidence_json TEXT NOT NULL,
		status TEXT NOT NULL,
		verified_at TEXT NOT NULL,
		UNIQUE(version_id, grant_id, use, context_key),
		FOREIGN KEY(credential_id) REFERENCES credentials(id),
		FOREIGN KEY(version_id) REFERENCES credential_versions(id),
		FOREIGN KEY(grant_id) REFERENCES credential_grants(id)
	);
CREATE TABLE credential_versions (
			id TEXT PRIMARY KEY,
			credential_id TEXT NOT NULL,
			payload_ciphertext TEXT NOT NULL,
			payload_digest TEXT NOT NULL,
			state TEXT NOT NULL,
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			promoted_at TEXT NOT NULL DEFAULT '',
			revoked_at TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(credential_id) REFERENCES credentials(id),
			FOREIGN KEY(created_by) REFERENCES users(id)
		);
CREATE TABLE credentials (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			scope TEXT NOT NULL,
			owner_id TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'active',
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT '',
			UNIQUE(scope, owner_id, name),
			FOREIGN KEY(owner_id) REFERENCES users(id),
			FOREIGN KEY(created_by) REFERENCES users(id)
		);
CREATE TABLE detached_tasks (
			audit_id INTEGER PRIMARY KEY,
			operation_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			session TEXT NOT NULL,
			command_summary TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL DEFAULT '',
			finished_at_ms INTEGER NOT NULL DEFAULT 0,
			task_id TEXT NOT NULL DEFAULT '',
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			output TEXT NOT NULL DEFAULT ''
		);
CREATE TABLE edge_agent_status (
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'offline',
			sessions INTEGER NOT NULL DEFAULT 0,
			remote TEXT NOT NULL DEFAULT '',
			generation INTEGER NOT NULL DEFAULT 0,
			connected_at TEXT NOT NULL DEFAULT '',
			last_seen TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(cluster, instance)
		);
CREATE TABLE edge_enrollments (
			id TEXT PRIMARY KEY,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			prefix TEXT NOT NULL,
			hash TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
CREATE TABLE edge_route_revision (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			version INTEGER NOT NULL
		);
CREATE TABLE edge_routes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hostname TEXT NOT NULL,
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			backend TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'tls-passthrough',
			enabled INTEGER NOT NULL DEFAULT 1,
			max_conns INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
CREATE TABLE execution_records (
			execution_id TEXT PRIMARY KEY,
			tool TEXT NOT NULL DEFAULT '',
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			agent_boot_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'running',
			error_text TEXT NOT NULL DEFAULT '',
			terminal TEXT NOT NULL DEFAULT '',
			terminal_truncated INTEGER NOT NULL DEFAULT 0,
			terminal_bytes INTEGER NOT NULL DEFAULT 0,
			created_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			finished_at_ms INTEGER NOT NULL DEFAULT 0,
			expires_at_ms INTEGER NOT NULL DEFAULT 0
		);
CREATE TABLE gateway_schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
CREATE TABLE gateway_secret_key_check (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			key_mac TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
CREATE TABLE git_repos (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
				branch TEXT NOT NULL DEFAULT 'main',
				username TEXT NOT NULL DEFAULT '',
				password_hash TEXT NOT NULL DEFAULT '',
				password_ciphertext TEXT NOT NULL DEFAULT '',
				description TEXT NOT NULL DEFAULT '',
				last_used_at TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
CREATE TABLE "grants" (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(user_id, cluster, instance),
			FOREIGN KEY(user_id) REFERENCES users(id)
		);
CREATE TABLE inspection_items (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES inspection_runs(id), asset_id TEXT NOT NULL,
 cluster TEXT NOT NULL, instance TEXT NOT NULL, cluster_id TEXT NOT NULL, check_id TEXT NOT NULL,
 generation INTEGER NOT NULL DEFAULT 1, opened INTEGER NOT NULL, deadline INTEGER NOT NULL,
 status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', repair TEXT NOT NULL DEFAULT '', repair_by TEXT NOT NULL DEFAULT '', repair_at INTEGER NOT NULL DEFAULT 0,
 UNIQUE(run_id,asset_id,cluster,instance,check_id)
) STRICT;
CREATE TABLE inspection_metric_points (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			collected_at_ms INTEGER NOT NULL DEFAULT 0,
			run_id TEXT NOT NULL DEFAULT '',
			schedule_id TEXT NOT NULL DEFAULT '',
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			skill_slug TEXT NOT NULL DEFAULT '',
			metric_key TEXT NOT NULL,
			metric_label TEXT NOT NULL DEFAULT '',
			value REAL NOT NULL DEFAULT 0,
			unit TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT ''
		);
CREATE TABLE inspection_plans (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL, owner TEXT NOT NULL, executor TEXT NOT NULL REFERENCES users(id),
 timezone TEXT NOT NULL, weekday INTEGER NOT NULL CHECK(weekday BETWEEN 0 AND 6), clock TEXT NOT NULL,
 next_due INTEGER NOT NULL, active INTEGER NOT NULL DEFAULT 1
) STRICT;
CREATE TABLE inspection_receipts (
 item_id INTEGER NOT NULL REFERENCES inspection_items(id), generation INTEGER NOT NULL, actor TEXT NOT NULL,
 received INTEGER NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL, evidence TEXT NOT NULL, digest TEXT NOT NULL,
 PRIMARY KEY(item_id,generation)
) STRICT;
CREATE TABLE inspection_runs (
 id INTEGER PRIMARY KEY, plan_id INTEGER NOT NULL REFERENCES inspection_plans(id), due INTEGER NOT NULL,
 frozen INTEGER NOT NULL, trigger_kind TEXT NOT NULL CHECK(trigger_kind IN ('manual','scheduled','reconciled'))
) STRICT;
CREATE TABLE inspection_scope (
 plan_id INTEGER NOT NULL REFERENCES inspection_plans(id), asset_id TEXT NOT NULL, cluster TEXT NOT NULL,
 instance TEXT NOT NULL, cluster_id TEXT NOT NULL, check_id TEXT NOT NULL,
 PRIMARY KEY(plan_id,asset_id,cluster,instance,check_id)
) STRICT;
CREATE TABLE inventory_business_deployments (
    asset_id TEXT NOT NULL REFERENCES inventory_business_systems(asset_id),
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    namespace TEXT NOT NULL,
    application TEXT NOT NULL,
    database_name TEXT NOT NULL DEFAULT '',
    verified_at TEXT NOT NULL,
    verified_by TEXT NOT NULL,
    PRIMARY KEY (asset_id, cluster, instance, namespace, application, database_name)
);
CREATE TABLE inventory_business_systems (
    asset_id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    owner TEXT NOT NULL,
    active INTEGER NOT NULL CHECK (active IN (0,1)),
    revision INTEGER NOT NULL CHECK (revision > 0)
);
CREATE TABLE inventory_import_attempts (
    attempt_id INTEGER PRIMARY KEY,
    batch_id TEXT NOT NULL,
    receipt TEXT NOT NULL
) STRICT;
CREATE TABLE inventory_machine_login_accounts (
    asset_id TEXT NOT NULL REFERENCES inventory_machines(asset_id),
    username TEXT NOT NULL,
    credential_id TEXT NOT NULL UNIQUE REFERENCES credentials(id),
    version_id TEXT NOT NULL UNIQUE REFERENCES credential_versions(id),
    PRIMARY KEY (asset_id, username)
) STRICT;
CREATE TABLE inventory_machine_login_key (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    key_mac TEXT NOT NULL
) STRICT;
CREATE TABLE inventory_machines (
    asset_id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL CHECK (length(name) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('physical', 'vm')),
    address TEXT NOT NULL DEFAULT '',
    cpu_count INTEGER CHECK (cpu_count > 0),
    memory_gib REAL CHECK (memory_gib > 0),
    disk_gib REAL CHECK (disk_gib > 0),
    host_asset_id TEXT REFERENCES inventory_machines(asset_id),
    cluster TEXT NOT NULL DEFAULT '',
    instance TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0), active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)), created_at TEXT NOT NULL DEFAULT '',
    CHECK (host_asset_id IS NULL OR (kind = 'vm' AND host_asset_id <> asset_id)),
    CHECK ((cluster = '') = (instance = ''))
) STRICT;
CREATE TABLE inventory_node_links (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    node_name TEXT NOT NULL CHECK (length(node_name) BETWEEN 1 AND 253),
    machine_id TEXT NOT NULL REFERENCES inventory_machines(asset_id),
    PRIMARY KEY (cluster, instance, node_name),
    FOREIGN KEY (cluster, instance) REFERENCES cluster_inventory_sources(cluster, instance)
) STRICT;
CREATE TABLE machine_diagnostic_bindings (
    asset_id TEXT PRIMARY KEY NOT NULL REFERENCES inventory_machines(asset_id),
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    host_sha256 TEXT NOT NULL UNIQUE CHECK(length(host_sha256)=64 AND host_sha256 NOT GLOB '*[^0-9a-f]*')
);
CREATE TABLE machine_diagnostic_samples (
    asset_id TEXT NOT NULL REFERENCES machine_diagnostic_bindings(asset_id),
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    host_sha256 TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    generation INTEGER NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (asset_id, at_ms)
) STRICT;
CREATE TABLE monitor_resource_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			asset_id TEXT NOT NULL,
			asset_name TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL,
			strategy TEXT NOT NULL,
			collector_method TEXT NOT NULL,
			collected_at_ms INTEGER NOT NULL,
			run_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'ok',
			cpu_cores REAL,
			cpu_total_cores REAL,
			cpu_percent REAL,
			memory_used_bytes REAL,
			memory_total_bytes REAL,
			memory_percent REAL,
			disk_used_bytes REAL,
			disk_total_bytes REAL,
			disk_percent REAL,
			net_bytes_per_sec REAL,
			disk_iops REAL,
			disk_latency_ms REAL,
			gpu_percent REAL,
			response_time_ms REAL,
			error_rate REAL,
			active_connections REAL,
			requests_per_sec REAL,
			uptime_seconds REAL,
			load_1m REAL,
			os_info TEXT NOT NULL DEFAULT '',
			kernel_info TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '[]',
			details_json TEXT NOT NULL DEFAULT '{}'
		);
CREATE TABLE monitor_run_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			asset_id TEXT NOT NULL,
			asset_name TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL,
			strategy TEXT NOT NULL,
			collector_method TEXT NOT NULL,
			status TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			started_at_ms INTEGER NOT NULL,
			finished_at_ms INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			metrics_json TEXT NOT NULL DEFAULT '{}',
			checks_json TEXT NOT NULL DEFAULT '[]',
			evidence_json TEXT NOT NULL DEFAULT '{}'
		, artifact_format TEXT NOT NULL DEFAULT '', artifact_markdown TEXT NOT NULL DEFAULT '', artifact_json TEXT NOT NULL DEFAULT '', raw_output TEXT NOT NULL DEFAULT '', artifact_dir TEXT NOT NULL DEFAULT '', artifact_bytes INTEGER NOT NULL DEFAULT 0, artifact_sha256 TEXT NOT NULL DEFAULT '', artifact_truncated INTEGER NOT NULL DEFAULT 0);
CREATE TABLE monitor_runs (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL DEFAULT '',
			schedule_name TEXT NOT NULL DEFAULT '',
			trigger_kind TEXT NOT NULL,
			triggered_by TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			total_targets INTEGER NOT NULL DEFAULT 0,
			total_assets INTEGER NOT NULL DEFAULT 0,
			passed_checks INTEGER NOT NULL DEFAULT 0,
			warning_checks INTEGER NOT NULL DEFAULT 0,
			failed_checks INTEGER NOT NULL DEFAULT 0,
			skipped_checks INTEGER NOT NULL DEFAULT 0,
			offline_targets INTEGER NOT NULL DEFAULT 0,
			metrics_written INTEGER NOT NULL DEFAULT 0,
			started_at_ms INTEGER NOT NULL,
			finished_at_ms INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			summary TEXT NOT NULL DEFAULT ''
		, prompt TEXT NOT NULL DEFAULT '', command TEXT NOT NULL DEFAULT '', skills_json TEXT NOT NULL DEFAULT '[]');
CREATE TABLE monitor_schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			scope_mode TEXT NOT NULL DEFAULT 'all_online',
			targets_json TEXT NOT NULL DEFAULT '[]',
			tags_json TEXT NOT NULL DEFAULT '[]',
			strategies_json TEXT NOT NULL DEFAULT '["vm_host","k8s_cluster","container_runtime","app_service","database_service"]',
			thresholds_json TEXT NOT NULL DEFAULT '{}',
			interval_seconds INTEGER NOT NULL DEFAULT 300,
			next_run_at_ms INTEGER NOT NULL DEFAULT 0,
			last_run_at_ms INTEGER NOT NULL DEFAULT 0,
			last_run_id TEXT NOT NULL DEFAULT '',
			last_status TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		, skills_json TEXT NOT NULL DEFAULT '[]', prompt TEXT NOT NULL DEFAULT '', command TEXT NOT NULL DEFAULT '', inputs_json TEXT NOT NULL DEFAULT '{}');
CREATE TABLE platform_load_samples (
			at_ms INTEGER PRIMARY KEY,
			online_agents INTEGER NOT NULL DEFAULT 0,
			busy_agents INTEGER NOT NULL DEFAULT 0,
			active_ops INTEGER NOT NULL DEFAULT 0,
			queued_ops INTEGER NOT NULL DEFAULT 0,
			active_sessions INTEGER NOT NULL DEFAULT 0,
			resource_locks INTEGER NOT NULL DEFAULT 0
		);
CREATE TABLE platform_mcp_servers (
			slug TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT 'custom',
			transport TEXT NOT NULL DEFAULT 'stdio',
			command TEXT NOT NULL DEFAULT '',
			args_json TEXT NOT NULL DEFAULT '[]',
			url TEXT NOT NULL DEFAULT '',
			env_json TEXT NOT NULL DEFAULT '{}',
			headers_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			auto_start INTEGER NOT NULL DEFAULT 1,
			tool_timeout_secs INTEGER NOT NULL DEFAULT 120,
			last_probe_status TEXT NOT NULL DEFAULT '',
			last_probe_error TEXT NOT NULL DEFAULT '',
			last_probe_at_ms INTEGER NOT NULL DEFAULT 0,
			last_latency_ms INTEGER NOT NULL DEFAULT 0,
			tools_json TEXT NOT NULL DEFAULT '[]',
			owner_id TEXT NOT NULL DEFAULT '',
			owner_name TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0
		);
CREATE TABLE platform_skills (
			slug TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT 'custom',
			category TEXT NOT NULL DEFAULT 'inspection',
			driver_mode TEXT NOT NULL DEFAULT 'agent_prompt',
			strategy_id TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			skill_md TEXT NOT NULL,
			files_json TEXT NOT NULL DEFAULT '[]',
			sha256 TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			owner_name TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			last_run_at_ms INTEGER NOT NULL DEFAULT 0,
			run_count INTEGER NOT NULL DEFAULT 0,
			file_count INTEGER NOT NULL DEFAULT 0,
			storage_dir TEXT NOT NULL DEFAULT '',
			ui_manifest_json TEXT NOT NULL DEFAULT '',
			ui_manifest_error TEXT NOT NULL DEFAULT ''
		);
CREATE TABLE proxy_routes (
			id TEXT PRIMARY KEY,
			host TEXT NOT NULL UNIQUE,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			upstream TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
CREATE TABLE release_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			release_number INTEGER NOT NULL,
			type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT '',
			stage TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY(release_number) REFERENCES release_tickets(number)
		);
CREATE TABLE release_tickets (
			number INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL DEFAULT '',
			token_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			requirement TEXT NOT NULL,
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT '',
			application TEXT NOT NULL DEFAULT '',
			environment TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL DEFAULT '',
			release_name TEXT NOT NULL DEFAULT '',
			plan_digest TEXT NOT NULL DEFAULT '',
			source_revision TEXT NOT NULL DEFAULT '',
			workspace_commit TEXT NOT NULL DEFAULT '',
			execution_mode TEXT NOT NULL DEFAULT '',
			instruction TEXT NOT NULL DEFAULT '',
			required_evidence_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'queued',
			stage TEXT NOT NULL DEFAULT '',
			superseded_by INTEGER,
			result_json TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT '',
			finished_at TEXT NOT NULL DEFAULT ''
		);
CREATE TABLE scheduler_issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			repo_slug TEXT NOT NULL,
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			issue_url TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'created',
			created_at TEXT NOT NULL
		);
CREATE TABLE scheduler_job_leases (
			job_id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			lease_until TEXT NOT NULL,
			started_at TEXT NOT NULL
		);
CREATE TABLE scheduler_jobs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			cluster_glob TEXT NOT NULL DEFAULT '*',
			instance_glob TEXT NOT NULL DEFAULT '*',
			interval_sec INTEGER NOT NULL DEFAULT 3600,
			scan_mode TEXT NOT NULL DEFAULT 'ask',
			scan_config TEXT NOT NULL DEFAULT '{}',
			platform TEXT NOT NULL DEFAULT 'cnb',
			repo_slug TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			token_env TEXT NOT NULL DEFAULT '',
			api_base TEXT NOT NULL DEFAULT '',
			dedup_window_sec INTEGER NOT NULL DEFAULT 86400,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_run_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
CREATE TABLE tokens (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			user_id TEXT,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			hash TEXT NOT NULL,
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT '',
			revoked_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, issued_via TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(user_id) REFERENCES users(id)
		);
CREATE TABLE upgrade_operations (
			id TEXT PRIMARY KEY,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			image TEXT NOT NULL,
			old_generation INTEGER NOT NULL,
			old_runtime_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			phase TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
CREATE TABLE users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		, password_hash TEXT NOT NULL DEFAULT '', account_type TEXT NOT NULL DEFAULT 'person', is_admin INTEGER NOT NULL DEFAULT 0);
CREATE TABLE agent_resource_state (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT '' CHECK (mode IN ('', 'kubernetes', 'docker', 'linux')),
    kubernetes_available INTEGER CHECK (kubernetes_available IN (0, 1)),
    supported INTEGER NOT NULL DEFAULT 0 CHECK (supported IN (0, 1)),
    manifest_revision INTEGER NOT NULL DEFAULT 0 CHECK (manifest_revision >= 0),
    manifest_hash TEXT NOT NULL DEFAULT '',
    generated_at TEXT NOT NULL DEFAULT '',
    connection_at TEXT NOT NULL DEFAULT '',
    observation_hash TEXT NOT NULL DEFAULT '',
    last_observed_at TEXT NOT NULL DEFAULT '',
    applied_revision INTEGER NOT NULL DEFAULT 0 CHECK (applied_revision >= 0),
    PRIMARY KEY(cluster, instance),
    FOREIGN KEY(cluster, instance) REFERENCES agent_status(cluster, instance) ON DELETE CASCADE
) STRICT;
CREATE TABLE agent_observations (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('machine', 'cluster', 'namespace', 'container', 'site', 'database')),
    "key" TEXT NOT NULL CHECK (length("key") BETWEEN 1 AND 128),
    details TEXT NOT NULL CHECK (json_valid(details) AND json_type(details) = 'object'),
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation >= 0),
    misses INTEGER NOT NULL DEFAULT 0 CHECK (misses >= 0),
    PRIMARY KEY(cluster, instance, kind, "key"),
    FOREIGN KEY(cluster, instance) REFERENCES agent_resource_state(cluster, instance) ON DELETE CASCADE
) STRICT;
CREATE TABLE agent_resource_audit (
    audit_id INTEGER PRIMARY KEY REFERENCES audit_events(id) ON DELETE CASCADE,
    manifest_revision INTEGER NOT NULL CHECK (manifest_revision >= 0),
    resource_refs_json TEXT NOT NULL CHECK (json_valid(resource_refs_json) AND json_type(resource_refs_json) = 'array')
) STRICT;
CREATE TABLE inventory_cluster_profiles (
    asset_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    environment TEXT NOT NULL,
    contact TEXT NOT NULL,
    created_by TEXT NOT NULL,
    revision INTEGER NOT NULL
) STRICT;
CREATE TABLE inventory_resource_catalog (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    created_by TEXT NOT NULL,
    name TEXT NOT NULL,
    environment TEXT NOT NULL,
    details TEXT NOT NULL CHECK (json_valid(details) AND json_type(details) = 'object'),
    identity_key TEXT NOT NULL,
    revision INTEGER NOT NULL
) STRICT;
CREATE TABLE inventory_agent_assets (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('machine','cluster','system')),
    asset_id TEXT NOT NULL,
    PRIMARY KEY(cluster,instance,kind,asset_id)
) STRICT;
CREATE TABLE inventory_machine_tags (
    asset_id TEXT NOT NULL REFERENCES inventory_machines(asset_id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY(asset_id, tag)
);
CREATE TABLE personal_asset_records (
    owner TEXT NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    name TEXT NOT NULL,
    environment TEXT NOT NULL,
    revision INTEGER NOT NULL,
    PRIMARY KEY(owner, kind, asset_id)
);
CREATE TRIGGER asset_runtime_deployment_drop AFTER DELETE ON inventory_business_deployments WHEN NOT EXISTS (
 SELECT 1 FROM inventory_business_deployments WHERE (cluster,instance,namespace,application)=(OLD.cluster,OLD.instance,OLD.namespace,OLD.application) AND verified_at<>'') BEGIN
 DELETE FROM asset_runtime_bindings WHERE (cluster,instance,namespace,application)=(OLD.cluster,OLD.instance,OLD.namespace,OLD.application);
END;
CREATE TRIGGER asset_runtime_ns_drop AFTER DELETE ON cluster_inventory_namespaces BEGIN
 DELETE FROM asset_runtime_snapshots WHERE (cluster,instance,namespace)=(OLD.cluster,OLD.instance,OLD.name);
 DELETE FROM asset_runtime_bindings WHERE (cluster,instance,namespace)=(OLD.cluster,OLD.instance,OLD.name);
END;
CREATE TRIGGER asset_runtime_ns_recreated AFTER UPDATE OF uid ON cluster_inventory_namespaces WHEN OLD.uid<>'' AND OLD.uid<>NEW.uid BEGIN
 DELETE FROM asset_runtime_snapshots WHERE (cluster,instance,namespace)=(OLD.cluster,OLD.instance,OLD.name);
 DELETE FROM asset_runtime_bindings WHERE (cluster,instance,namespace)=(OLD.cluster,OLD.instance,OLD.name);
END;
CREATE TRIGGER asset_runtime_source_drop AFTER DELETE ON cluster_inventory_sources BEGIN
 DELETE FROM asset_runtime_snapshots WHERE (cluster,instance)=(OLD.cluster,OLD.instance);
 DELETE FROM asset_runtime_bindings WHERE (cluster,instance)=(OLD.cluster,OLD.instance);
END;
CREATE TRIGGER inventory_node_links_check_insert BEFORE INSERT ON inventory_node_links
WHEN NOT EXISTS (SELECT 1 FROM inventory_machines WHERE asset_id = NEW.machine_id)
  OR NOT EXISTS (SELECT 1 FROM cluster_inventory_sources WHERE (cluster, instance) = (NEW.cluster, NEW.instance))
BEGIN SELECT RAISE(ABORT, 'missing node link parent'); END;
CREATE TRIGGER inventory_node_links_check_update BEFORE UPDATE OF machine_id, cluster, instance ON inventory_node_links
WHEN NOT EXISTS (SELECT 1 FROM inventory_machines WHERE asset_id = NEW.machine_id)
  OR NOT EXISTS (SELECT 1 FROM cluster_inventory_sources WHERE (cluster, instance) = (NEW.cluster, NEW.instance))
BEGIN SELECT RAISE(ABORT, 'missing node link parent'); END;
CREATE TRIGGER inventory_node_links_machine_delete BEFORE DELETE ON inventory_machines
WHEN EXISTS (SELECT 1 FROM inventory_node_links WHERE machine_id = OLD.asset_id)
BEGIN SELECT RAISE(ABORT, 'machine has node links'); END;
CREATE TRIGGER inventory_node_links_machine_rekey BEFORE UPDATE OF asset_id ON inventory_machines
WHEN NEW.asset_id <> OLD.asset_id AND EXISTS (SELECT 1 FROM inventory_node_links WHERE machine_id = OLD.asset_id)
BEGIN SELECT RAISE(ABORT, 'machine has node links'); END;
CREATE TRIGGER inventory_node_links_source_delete BEFORE DELETE ON cluster_inventory_sources
WHEN EXISTS (SELECT 1 FROM inventory_node_links WHERE (cluster, instance) = (OLD.cluster, OLD.instance))
BEGIN SELECT RAISE(ABORT, 'cluster source has node links'); END;
CREATE TRIGGER inventory_node_links_source_rekey BEFORE UPDATE OF asset_id, cluster, instance ON cluster_inventory_sources
WHEN (NEW.asset_id, NEW.cluster, NEW.instance) <> (OLD.asset_id, OLD.cluster, OLD.instance)
 AND EXISTS (SELECT 1 FROM inventory_node_links WHERE (cluster, instance) = (OLD.cluster, OLD.instance))
BEGIN SELECT RAISE(ABORT, 'cluster source has node links'); END;
CREATE TRIGGER machine_diagnostic_asset_deleted BEFORE DELETE ON inventory_machines
BEGIN
    DELETE FROM machine_diagnostic_samples WHERE asset_id=OLD.asset_id;
    DELETE FROM machine_diagnostic_bindings WHERE asset_id=OLD.asset_id;
END;
CREATE TRIGGER machine_diagnostic_target_changed AFTER UPDATE OF cluster,instance ON inventory_machines
WHEN (OLD.cluster, OLD.instance) <> (NEW.cluster, NEW.instance)
BEGIN
    DELETE FROM machine_diagnostic_samples WHERE asset_id=OLD.asset_id;
    DELETE FROM machine_diagnostic_bindings WHERE asset_id=OLD.asset_id;
END;
CREATE TABLE platform_events (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			occurred_at_ms INTEGER NOT NULL,
			occurred_at TEXT NOT NULL,
			actor_user_id TEXT NOT NULL DEFAULT '',
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			correlation_id TEXT NOT NULL DEFAULT '',
			body_json TEXT NOT NULL,
			dedupe_key TEXT,
			created_at TEXT NOT NULL
		);
CREATE UNIQUE INDEX idx_platform_events_dedupe ON platform_events(dedupe_key);
CREATE INDEX idx_platform_events_time ON platform_events(occurred_at_ms DESC, id DESC);
CREATE INDEX idx_platform_events_type ON platform_events(type, occurred_at_ms DESC);
CREATE TABLE webhook_endpoints (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			url_ciphertext TEXT NOT NULL,
			url_host TEXT NOT NULL,
			url_sha256 TEXT NOT NULL,
			secret_ciphertext TEXT NOT NULL,
			previous_secret_ciphertext TEXT NOT NULL DEFAULT '',
			previous_secret_expires_at_ms INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			scope_cluster TEXT NOT NULL DEFAULT '',
			scope_instance TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_success_at_ms INTEGER NOT NULL DEFAULT 0,
			last_failure_at_ms INTEGER NOT NULL DEFAULT 0,
			last_error_category TEXT NOT NULL DEFAULT '',
			deleted_at TEXT NOT NULL DEFAULT ''
		);
CREATE INDEX idx_webhook_endpoints_active ON webhook_endpoints(enabled, deleted_at, created_at DESC);
CREATE TABLE webhook_subscriptions (
			endpoint_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (endpoint_id, event_type)
		);
CREATE INDEX idx_webhook_subscriptions_event ON webhook_subscriptions(event_type, endpoint_id);
CREATE TABLE platform_plugin_config (
			id TEXT PRIMARY KEY CHECK (id = 'default'),
			scenario_id TEXT NOT NULL DEFAULT 'all-in-one',
			revision INTEGER NOT NULL DEFAULT 0,
			updated_by TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL DEFAULT 0
		);
CREATE TABLE platform_plugin_overrides (
			plugin_id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			updated_by TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL DEFAULT 0
		);
CREATE TABLE webhook_deliveries (
			id TEXT PRIMARY KEY,
			endpoint_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			status TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 0,
			next_attempt_at_ms INTEGER NOT NULL,
			lease_token TEXT NOT NULL DEFAULT '',
			lease_until_ms INTEGER NOT NULL DEFAULT 0,
			last_response_status INTEGER NOT NULL DEFAULT 0,
			error_category TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL,
			updated_at_ms INTEGER NOT NULL,
			delivered_at_ms INTEGER NOT NULL DEFAULT 0
		);
CREATE UNIQUE INDEX idx_webhook_deliveries_event ON webhook_deliveries(endpoint_id, event_id);
CREATE INDEX idx_webhook_deliveries_due ON webhook_deliveries(status, next_attempt_at_ms);
CREATE INDEX idx_webhook_deliveries_endpoint ON webhook_deliveries(endpoint_id, created_at_ms DESC);
