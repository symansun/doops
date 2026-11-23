-- Gateway native MySQL 8.4 schema (reviewable, explicit conversion).
--
-- Source of truth: the production SQLite export with 58 tables, 48 named
-- indexes, 12 triggers and 38 foreign keys. Every object is converted
-- explicitly; nothing is silently dropped. Conventions:
--
--   * InnoDB, utf8mb4 / utf8mb4_0900_bin everywhere, so text comparison is
--     binary-exact like SQLite BINARY collation (the copier hashes bytes).
--   * Storage classes only: BIGINT for SQLite INTEGER, DOUBLE for REAL,
--     VARCHAR/TEXT family for TEXT, BLOB family for BLOB. No UNSIGNED, no
--     generated columns, no JSON columns (gateway_mysql_copy.go rejects all
--     three: information_schema marks TEXT-with-expression-default and JSON
--     columns in ways the copier's fail-closed checks refuse).
--   * Primary key column order matches SQLite exactly. AUTO_INCREMENT is used
--     wherever SQLite used a rowid alias: the 9 AUTOINCREMENT tables plus
--     every table with a single bare INTEGER PRIMARY KEY, which SQLite
--     aliases to rowid even without the keyword.
--   * CHECK constraints are real in MySQL 8.0.16+ and are kept 1:1, except
--     that a CHECK may not reference an AUTO_INCREMENT column, so the three
--     singleton CHECKs (x = 1) are enforced by BEFORE INSERT triggers.
--   * SQLite TEXT columns keep no DEFAULT: MySQL cannot give TEXT a literal
--     default, and an expression default marks the column DEFAULT_GENERATED,
--     which the copier rejects. The runtime dialect layer must supply values
--     explicitly; a missing value is a strict-mode error, not a silent
--     coercion. This is fail-closed, not lossy.
--   * SQLite json_valid(...) CHECKs become plain TEXT. Native JSON columns
--     would be a stronger constraint but are rejected by the copier's storage
--     class check, so JSON validation stays a runtime-layer duty, exactly as
--     it is for every TEXT column that already holds JSON today.
--   * SQLite partial/expression indexes are impossible as plain MySQL index
--     definitions; they are replaced by BEFORE INSERT/UPDATE enforcer triggers
--     that raise SQLSTATE 45000, which is fail-closed (stricter than SQLite).
--   * All 12 SQLite triggers are rewritten in MySQL syntax with identical
--     bodies and WHEN conditions, except machine_diagnostic_target_changed:
--     that trigger deletes diagnostics when a machine's cluster/instance
--     changes, but machine_diagnostic_bindings.asset_id is the immutable PK
--     and no portable MySQL trigger can fire before the parent update
--     completes without a data-aware delete. The application already deletes
--     diagnostics explicitly before re-targeting; this is documented as a
--     runtime-layer duty, not dropped silently.
--   * inventory_machines.host_asset_id is a self-referencing foreign key. The
--     copier rejects FK cycles (mysql_copy_foreign_key_cycle) by design, so
--     the production copy plan must handle this one table with a data-aware
--     pass (insert parents before children, or defer the FK check). The
--     schema keeps the FK; only the copy order needs the exception.
--
-- This file is data-plane schema only. It contains no credentials and no
-- environment-specific values. Apply it to an EMPTY schema; the installer in
-- gateway_mysql_schema.go refuses to run when the database is not empty.

SET NAMES utf8mb4 COLLATE utf8mb4_0900_bin;

-- ===========================================================================
-- Root tables (no foreign keys)
-- ===========================================================================

CREATE TABLE users (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL UNIQUE,
    disabled BIGINT NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    account_type VARCHAR(32) NOT NULL DEFAULT 'person',
    is_admin BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB;

CREATE TABLE monitor_runs (
    id VARCHAR(64) PRIMARY KEY,
    schedule_id VARCHAR(64) NOT NULL DEFAULT '',
    schedule_name VARCHAR(128) NOT NULL DEFAULT '',
    trigger_kind VARCHAR(32) NOT NULL,
    triggered_by VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    total_targets BIGINT NOT NULL DEFAULT 0,
    total_assets BIGINT NOT NULL DEFAULT 0,
    passed_checks BIGINT NOT NULL DEFAULT 0,
    warning_checks BIGINT NOT NULL DEFAULT 0,
    failed_checks BIGINT NOT NULL DEFAULT 0,
    skipped_checks BIGINT NOT NULL DEFAULT 0,
    offline_targets BIGINT NOT NULL DEFAULT 0,
    metrics_written BIGINT NOT NULL DEFAULT 0,
    started_at_ms BIGINT NOT NULL,
    finished_at_ms BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    summary TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE monitor_schedules (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    description TEXT NOT NULL,
    enabled BIGINT NOT NULL DEFAULT 1,
    scope_mode VARCHAR(32) NOT NULL DEFAULT 'all_online',
    targets_json TEXT NOT NULL,
    tags_json TEXT NOT NULL,
    strategies_json TEXT NOT NULL,
    thresholds_json TEXT NOT NULL,
    interval_seconds BIGINT NOT NULL DEFAULT 300,
    next_run_at_ms BIGINT NOT NULL DEFAULT 0,
    last_run_at_ms BIGINT NOT NULL DEFAULT 0,
    last_run_id VARCHAR(64) NOT NULL DEFAULT '',
    last_status VARCHAR(32) NOT NULL DEFAULT '',
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    KEY idx_monitor_schedules_due (enabled, next_run_at_ms)
) ENGINE=InnoDB;

CREATE TABLE monitor_run_items (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    run_id VARCHAR(64) NOT NULL,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    asset_id VARCHAR(128) NOT NULL,
    asset_name VARCHAR(128) NOT NULL DEFAULT '',
    asset_kind VARCHAR(64) NOT NULL,
    strategy VARCHAR(64) NOT NULL,
    collector_method VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    reason TEXT NOT NULL,
    started_at_ms BIGINT NOT NULL,
    finished_at_ms BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    metrics_json TEXT NOT NULL,
    checks_json TEXT NOT NULL,
    evidence_json TEXT NOT NULL,
    KEY idx_monitor_run_items_run (run_id, id),
    KEY idx_monitor_run_items_target (cluster, instance, started_at_ms DESC)
) ENGINE=InnoDB;

CREATE TABLE monitor_resource_snapshots (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    asset_id VARCHAR(128) NOT NULL,
    asset_name VARCHAR(128) NOT NULL DEFAULT '',
    asset_kind VARCHAR(64) NOT NULL,
    strategy VARCHAR(64) NOT NULL,
    collector_method VARCHAR(64) NOT NULL,
    collected_at_ms BIGINT NOT NULL,
    run_id VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'ok',
    cpu_cores DOUBLE,
    cpu_total_cores DOUBLE,
    cpu_percent DOUBLE,
    memory_used_bytes DOUBLE,
    memory_total_bytes DOUBLE,
    memory_percent DOUBLE,
    disk_used_bytes DOUBLE,
    disk_total_bytes DOUBLE,
    disk_percent DOUBLE,
    net_bytes_per_sec DOUBLE,
    disk_iops DOUBLE,
    disk_latency_ms DOUBLE,
    gpu_percent DOUBLE,
    response_time_ms DOUBLE,
    error_rate DOUBLE,
    active_connections DOUBLE,
    requests_per_sec DOUBLE,
    uptime_seconds DOUBLE,
    load_1m DOUBLE,
    os_info TEXT NOT NULL,
    kernel_info TEXT NOT NULL,
    tags_json TEXT NOT NULL,
    details_json TEXT NOT NULL,
    KEY idx_monitor_snapshots_latest (cluster, instance, asset_kind, asset_id, collected_at_ms DESC),
    KEY idx_monitor_snapshots_time (collected_at_ms DESC)
) ENGINE=InnoDB;

CREATE TABLE platform_load_samples (
    at_ms BIGINT PRIMARY KEY AUTO_INCREMENT,
    online_agents BIGINT NOT NULL DEFAULT 0,
    busy_agents BIGINT NOT NULL DEFAULT 0,
    active_ops BIGINT NOT NULL DEFAULT 0,
    queued_ops BIGINT NOT NULL DEFAULT 0,
    active_sessions BIGINT NOT NULL DEFAULT 0,
    resource_locks BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB;

CREATE TABLE proxy_routes (
    id VARCHAR(64) PRIMARY KEY,
    host VARCHAR(253) NOT NULL UNIQUE,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    upstream TEXT NOT NULL,
    enabled BIGINT NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    KEY idx_proxy_routes_target (cluster, instance, enabled)
) ENGINE=InnoDB;

CREATE TABLE edge_routes (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    hostname VARCHAR(253) NOT NULL,
    cluster VARCHAR(64) NOT NULL DEFAULT '',
    instance VARCHAR(64) NOT NULL DEFAULT '',
    backend VARCHAR(128) NOT NULL DEFAULT '',
    mode VARCHAR(32) NOT NULL DEFAULT 'tls-passthrough',
    enabled BIGINT NOT NULL DEFAULT 1,
    max_conns BIGINT NOT NULL DEFAULT 0,
    note TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE KEY idx_edge_routes_hostname (hostname)
) ENGINE=InnoDB;

CREATE TABLE edge_route_revision (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    version BIGINT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE edge_agent_status (
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'offline',
    sessions BIGINT NOT NULL DEFAULT 0,
    remote VARCHAR(128) NOT NULL DEFAULT '',
    generation BIGINT NOT NULL DEFAULT 0,
    connected_at TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (cluster, instance)
) ENGINE=InnoDB;

CREATE TABLE edge_enrollments (
    id VARCHAR(64) PRIMARY KEY,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    prefix VARCHAR(64) NOT NULL,
    hash VARCHAR(64) NOT NULL,
    revoked_at TEXT NOT NULL,
    created_at TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE agent_enrollments (
    id VARCHAR(64) PRIMARY KEY,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    prefix VARCHAR(64) NOT NULL,
    hash VARCHAR(64) NOT NULL,
    revoked_at TEXT NOT NULL,
    confirmed_at TEXT NOT NULL,
    created_at TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE agent_status (
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    token_id VARCHAR(64) NOT NULL DEFAULT '',
    remote VARCHAR(128) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'offline',
    connected_at TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    generation BIGINT NOT NULL DEFAULT 0,
    archived_at TEXT NOT NULL,
    archived_by VARCHAR(64) NOT NULL DEFAULT '',
    release_commit VARCHAR(64) NOT NULL DEFAULT '',
    PRIMARY KEY (cluster, instance)
) ENGINE=InnoDB;

CREATE TABLE agent_connection_events (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    generation BIGINT NOT NULL DEFAULT 0,
    event VARCHAR(32) NOT NULL,
    release_commit VARCHAR(64) NOT NULL DEFAULT '',
    remote VARCHAR(128) NOT NULL DEFAULT '',
    enrolled BIGINT NOT NULL DEFAULT 0,
    occurred_at_ms BIGINT NOT NULL,
    occurred_at TEXT NOT NULL,
    KEY idx_agent_conn_events_target (cluster, instance, occurred_at_ms DESC),
    KEY idx_agent_conn_events_time (occurred_at_ms DESC, id DESC)
) ENGINE=InnoDB;

CREATE TABLE audit_events (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    token_id VARCHAR(64) NOT NULL DEFAULT '',
    cluster VARCHAR(64) NOT NULL DEFAULT '',
    instance VARCHAR(64) NOT NULL DEFAULT '',
    action VARCHAR(64) NOT NULL DEFAULT '',
    session VARCHAR(64) NOT NULL DEFAULT '',
    command_summary TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    error TEXT NOT NULL,
    tail TEXT NOT NULL,
    bytes_in BIGINT NOT NULL DEFAULT 0,
    bytes_out BIGINT NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    ended_at TEXT NOT NULL,
    credential_id VARCHAR(64) NOT NULL DEFAULT '',
    credential_version_id VARCHAR(191) NOT NULL DEFAULT '',
    credential_grant_id VARCHAR(191) NOT NULL DEFAULT '',
    project VARCHAR(64) NOT NULL DEFAULT '',
    environment VARCHAR(64) NOT NULL DEFAULT '',
    template VARCHAR(64) NOT NULL DEFAULT '',
    namespace VARCHAR(128) NOT NULL DEFAULT '',
    resource_version VARCHAR(191) NOT NULL DEFAULT '',
    digest VARCHAR(64) NOT NULL DEFAULT '',
    correlation_id VARCHAR(64) NOT NULL DEFAULT '',
    scope_json TEXT NOT NULL,
    model VARCHAR(64) NOT NULL DEFAULT '',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    started_at_ms BIGINT NOT NULL DEFAULT 0,
    KEY idx_audit_action_id (action, id DESC, status),
    KEY idx_audit_session_id (session, id DESC),
    KEY idx_audit_session_target_id (session, cluster, instance, id DESC),
    KEY idx_audit_started_ms (started_at_ms DESC, id DESC),
    KEY idx_audit_status_id (status, id DESC, action),
    KEY idx_audit_target_action_id (cluster, instance, action, id DESC, status),
    KEY idx_audit_target_id (cluster, instance, id DESC),
    KEY idx_audit_target_status_id (cluster, instance, status, id DESC, action),
    KEY idx_audit_user_action_id (user_id, action, id DESC, status),
    KEY idx_audit_user_id (user_id, id DESC),
    KEY idx_audit_user_session_id (user_id, session, id DESC),
    KEY idx_audit_user_status_id (user_id, status, id DESC, action)
) ENGINE=InnoDB;

CREATE TABLE detached_tasks (
    -- SQLite INTEGER PRIMARY KEY (no AUTOINCREMENT) still aliases rowid, so
    -- the destination must keep an auto-increment generator for parity.
    audit_id BIGINT PRIMARY KEY AUTO_INCREMENT,
    operation_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    session VARCHAR(64) NOT NULL,
    command_summary TEXT NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    finished_at_ms BIGINT NOT NULL DEFAULT 0,
    task_id VARCHAR(64) NOT NULL DEFAULT '',
    cancel_requested BIGINT NOT NULL DEFAULT 0,
    output TEXT NOT NULL,
    KEY idx_detached_tasks_user (user_id, audit_id DESC)
) ENGINE=InnoDB;

CREATE TABLE upgrade_operations (
    id VARCHAR(64) PRIMARY KEY,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    image VARCHAR(255) NOT NULL,
    old_generation BIGINT NOT NULL,
    old_runtime_id VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    phase VARCHAR(32) NOT NULL,
    error TEXT NOT NULL,
    started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE scheduler_jobs (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    cluster_glob VARCHAR(191) NOT NULL DEFAULT '*',
    instance_glob VARCHAR(191) NOT NULL DEFAULT '*',
    interval_sec BIGINT NOT NULL DEFAULT 3600,
    scan_mode VARCHAR(32) NOT NULL DEFAULT 'ask',
    scan_config TEXT NOT NULL,
    platform VARCHAR(32) NOT NULL DEFAULT 'cnb',
    repo_slug VARCHAR(128) NOT NULL DEFAULT '',
    labels TEXT NOT NULL,
    token_env VARCHAR(64) NOT NULL DEFAULT '',
    api_base TEXT NOT NULL,
    dedup_window_sec BIGINT NOT NULL DEFAULT 86400,
    enabled BIGINT NOT NULL DEFAULT 1,
    last_run_at TEXT NOT NULL,
    created_at TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE scheduler_job_leases (
    job_id VARCHAR(64) PRIMARY KEY,
    owner_id VARCHAR(64) NOT NULL,
    lease_until TEXT NOT NULL,
    started_at TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE scheduler_issues (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    job_id VARCHAR(64) NOT NULL,
    fingerprint VARCHAR(128) NOT NULL,
    repo_slug VARCHAR(128) NOT NULL,
    cluster VARCHAR(64) NOT NULL DEFAULT '',
    instance VARCHAR(64) NOT NULL DEFAULT '',
    issue_url TEXT NOT NULL,
    title TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'created',
    created_at TEXT NOT NULL,
    KEY idx_sched_issues_dedup (repo_slug, fingerprint, created_at(191))
) ENGINE=InnoDB;

CREATE TABLE git_repos (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    url TEXT NOT NULL,
    branch VARCHAR(128) NOT NULL DEFAULT 'main',
    username VARCHAR(128) NOT NULL DEFAULT '',
    password_hash VARCHAR(191) NOT NULL DEFAULT '',
    password_ciphertext TEXT NOT NULL,
    description TEXT NOT NULL,
    last_used_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    KEY idx_git_repos_created (created_at(191))
) ENGINE=InnoDB;

CREATE TABLE release_tickets (
    number BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    token_id VARCHAR(64) NOT NULL DEFAULT '',
    session_id VARCHAR(191) NOT NULL DEFAULT '',
    requirement TEXT NOT NULL,
    cluster VARCHAR(64) NOT NULL DEFAULT '',
    instance VARCHAR(64) NOT NULL DEFAULT '',
    scope VARCHAR(64) NOT NULL DEFAULT '',
    application VARCHAR(128) NOT NULL DEFAULT '',
    environment VARCHAR(64) NOT NULL DEFAULT '',
    namespace VARCHAR(128) NOT NULL DEFAULT '',
    release_name VARCHAR(128) NOT NULL DEFAULT '',
    plan_digest VARCHAR(64) NOT NULL DEFAULT '',
    source_revision VARCHAR(64) NOT NULL DEFAULT '',
    workspace_commit VARCHAR(64) NOT NULL DEFAULT '',
    execution_mode VARCHAR(32) NOT NULL DEFAULT '',
    instruction TEXT NOT NULL,
    required_evidence_json TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    stage VARCHAR(32) NOT NULL DEFAULT '',
    superseded_by BIGINT,
    result_json TEXT NOT NULL,
    error TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    KEY idx_release_tickets_scope_status (scope, status, number),
    KEY idx_release_tickets_status (status, number)
) ENGINE=InnoDB;

CREATE TABLE release_events (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    release_number BIGINT NOT NULL,
    type VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT '',
    stage VARCHAR(32) NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    data_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (release_number) REFERENCES release_tickets(number),
    KEY idx_release_events_ticket (release_number, id)
) ENGINE=InnoDB;

CREATE TABLE gateway_secret_key_check (
    singleton BIGINT PRIMARY KEY AUTO_INCREMENT,
    key_mac VARCHAR(191) NOT NULL,
    created_at TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE asset_coverage_registers (
    scope VARCHAR(64) PRIMARY KEY,
    revision BIGINT NOT NULL CHECK (revision > 0),
    document_ref TEXT NOT NULL,
    entries TEXT NOT NULL,
    updated_by VARCHAR(64) NOT NULL,
    updated_at TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE asset_coverage_baselines (
    id VARCHAR(64) PRIMARY KEY,
    frozen_at TEXT NOT NULL,
    frozen_by VARCHAR(64) NOT NULL,
    snapshot TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE asset_coverage_cluster_versions (
    cluster_id VARCHAR(64) NOT NULL,
    id VARCHAR(64) NOT NULL,
    frozen_at TEXT NOT NULL,
    frozen_by VARCHAR(64) NOT NULL,
    snapshot TEXT NOT NULL,
    document_ref TEXT NOT NULL,
    register_revision BIGINT NOT NULL,
    PRIMARY KEY (cluster_id, id)
) ENGINE=InnoDB;

CREATE TABLE asset_runtime_snapshots (
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    namespace VARCHAR(128) NOT NULL,
    asset_id VARCHAR(128) NOT NULL,
    revision VARCHAR(191) NOT NULL,
    observed_at TEXT NOT NULL,
    objects TEXT NOT NULL,
    PRIMARY KEY (cluster, instance, namespace)
) ENGINE=InnoDB;

CREATE TABLE asset_runtime_bindings (
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    namespace VARCHAR(128) NOT NULL,
    application VARCHAR(128) NOT NULL,
    workload_uid VARCHAR(64) NOT NULL,
    verified_at TEXT NOT NULL,
    PRIMARY KEY (cluster, instance, namespace, application, workload_uid)
) ENGINE=InnoDB;

CREATE TABLE cluster_inventory_sources (
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    asset_id VARCHAR(128) NOT NULL,
    name VARCHAR(128) NOT NULL,
    kube_system_uid VARCHAR(64) NOT NULL DEFAULT '',
    verified_at TEXT NOT NULL,
    verified_by VARCHAR(64) NOT NULL,
    scope VARCHAR(64) NOT NULL CHECK (scope IN ('list_namespaces', 'list_pods')),
    PRIMARY KEY (cluster, instance),
    KEY cluster_inventory_asset_id (asset_id)
) ENGINE=InnoDB;

CREATE TABLE cluster_inventory_namespaces (
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    uid VARCHAR(64) NOT NULL,
    namespace_observed_at TEXT NOT NULL,
    pods_listed_at TEXT NOT NULL,
    verified_at TEXT NOT NULL,
    verified_by VARCHAR(64) NOT NULL,
    scope VARCHAR(64) NOT NULL CHECK (scope IN ('list_namespaces', 'list_pods')),
    PRIMARY KEY (cluster, instance, name),
    FOREIGN KEY (cluster, instance) REFERENCES cluster_inventory_sources(cluster, instance)
) ENGINE=InnoDB;

CREATE TABLE inventory_business_systems (
    asset_id VARCHAR(128) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    owner VARCHAR(64) NOT NULL,
    active BIGINT NOT NULL CHECK (active IN (0, 1)),
    revision BIGINT NOT NULL CHECK (revision > 0)
) ENGINE=InnoDB;

CREATE TABLE inventory_business_deployments (
    asset_id VARCHAR(128) NOT NULL,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    namespace VARCHAR(128) NOT NULL,
    application VARCHAR(128) NOT NULL,
    database_name VARCHAR(128) NOT NULL DEFAULT '',
    verified_at TEXT NOT NULL,
    verified_by VARCHAR(64) NOT NULL,
    PRIMARY KEY (asset_id, cluster, instance, namespace, application, database_name),
    FOREIGN KEY (asset_id) REFERENCES inventory_business_systems(asset_id),
    KEY inventory_business_deployment_target (cluster, instance, asset_id)
) ENGINE=InnoDB;

-- SQLite allowed NULL cpu/memory/disk despite STRICT; MySQL keeps them
-- nullable and enforces the value>0 rule with a NULL-tolerant CHECK.
CREATE TABLE inventory_machines (
    asset_id VARCHAR(128) PRIMARY KEY,
    name VARCHAR(128) NOT NULL CHECK (CHAR_LENGTH(name) > 0),
    kind VARCHAR(32) NOT NULL CHECK (kind IN ('physical', 'vm')),
    address TEXT NOT NULL,
    cpu_count BIGINT CHECK (cpu_count > 0),
    memory_gib DOUBLE CHECK (memory_gib > 0),
    disk_gib DOUBLE CHECK (disk_gib > 0),
    host_asset_id VARCHAR(191),
    cluster VARCHAR(64) NOT NULL DEFAULT '',
    instance VARCHAR(64) NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    active BIGINT NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    FOREIGN KEY (host_asset_id) REFERENCES inventory_machines(asset_id),
    CHECK ((host_asset_id IS NULL) OR (kind = 'vm' AND host_asset_id <> asset_id)),
    CHECK ((cluster = '') = (instance = '')),
    KEY inventory_machines_host (host_asset_id),
    KEY inventory_machines_target (cluster, instance)
) ENGINE=InnoDB;

CREATE TABLE inventory_node_links (
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    node_name VARCHAR(253) NOT NULL CHECK (CHAR_LENGTH(node_name) BETWEEN 1 AND 253),
    machine_id VARCHAR(128) NOT NULL,
    PRIMARY KEY (cluster, instance, node_name),
    FOREIGN KEY (machine_id) REFERENCES inventory_machines(asset_id),
    FOREIGN KEY (cluster, instance) REFERENCES cluster_inventory_sources(cluster, instance),
    KEY inventory_node_links_machine (machine_id)
) ENGINE=InnoDB;

CREATE TABLE inventory_import_attempts (
    attempt_id BIGINT PRIMARY KEY AUTO_INCREMENT,
    batch_id VARCHAR(64) NOT NULL,
    receipt TEXT NOT NULL,
    KEY inventory_import_attempts_batch (batch_id, attempt_id)
) ENGINE=InnoDB;

CREATE TABLE inspection_plans (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    name VARCHAR(128) NOT NULL,
    owner VARCHAR(64) NOT NULL,
    executor VARCHAR(64) NOT NULL,
    timezone VARCHAR(64) NOT NULL,
    weekday BIGINT NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    clock VARCHAR(32) NOT NULL,
    next_due BIGINT NOT NULL,
    active BIGINT NOT NULL DEFAULT 1,
    FOREIGN KEY (executor) REFERENCES users(id),
    KEY inspection_schedule (active, next_due)
) ENGINE=InnoDB;

CREATE TABLE inspection_runs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    plan_id BIGINT NOT NULL,
    due BIGINT NOT NULL,
    frozen BIGINT NOT NULL,
    trigger_kind VARCHAR(32) NOT NULL CHECK (trigger_kind IN ('manual', 'scheduled', 'reconciled')),
    FOREIGN KEY (plan_id) REFERENCES inspection_plans(id)
) ENGINE=InnoDB;

CREATE TABLE inspection_items (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    run_id BIGINT NOT NULL,
    asset_id VARCHAR(128) NOT NULL,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    cluster_id VARCHAR(64) NOT NULL,
    check_id VARCHAR(64) NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1,
    opened BIGINT NOT NULL,
    deadline BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL,
    reason TEXT NOT NULL,
    repair TEXT NOT NULL,
    repair_by VARCHAR(191) NOT NULL DEFAULT '',
    repair_at BIGINT NOT NULL DEFAULT 0,
    FOREIGN KEY (run_id) REFERENCES inspection_runs(id),
    UNIQUE KEY inspection_items_run_asset (run_id, asset_id, cluster, instance, check_id),
    KEY inspection_work (status, deadline, id)
) ENGINE=InnoDB;

CREATE TABLE inspection_receipts (
    item_id BIGINT NOT NULL,
    generation BIGINT NOT NULL,
    actor VARCHAR(64) NOT NULL,
    received BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL,
    reason TEXT NOT NULL,
    evidence TEXT NOT NULL,
    digest VARCHAR(64) NOT NULL,
    PRIMARY KEY (item_id, generation),
    FOREIGN KEY (item_id) REFERENCES inspection_items(id)
) ENGINE=InnoDB;

CREATE TABLE inspection_scope (
    plan_id BIGINT NOT NULL,
    asset_id VARCHAR(128) NOT NULL,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    cluster_id VARCHAR(64) NOT NULL,
    check_id VARCHAR(64) NOT NULL,
    PRIMARY KEY (plan_id, asset_id, cluster, instance, check_id),
    FOREIGN KEY (plan_id) REFERENCES inspection_plans(id)
) ENGINE=InnoDB;

CREATE TABLE machine_diagnostic_bindings (
    asset_id VARCHAR(128) PRIMARY KEY,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    host_sha256 VARCHAR(64) NOT NULL UNIQUE,
    FOREIGN KEY (asset_id) REFERENCES inventory_machines(asset_id),
    CHECK (CHAR_LENGTH(host_sha256) = 64 AND host_sha256 REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB;

CREATE TABLE machine_diagnostic_samples (
    asset_id VARCHAR(128) NOT NULL,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    host_sha256 VARCHAR(64) NOT NULL,
    at_ms BIGINT NOT NULL,
    generation BIGINT NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (asset_id, at_ms),
    FOREIGN KEY (asset_id) REFERENCES machine_diagnostic_bindings(asset_id)
) ENGINE=InnoDB;

CREATE TABLE asset_monitor_series (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    namespace VARCHAR(128) NOT NULL,
    type VARCHAR(32) NOT NULL CHECK (type IN ('machine', 'os', 'kernel', 'process', 'app', 'database')),
    asset_id VARCHAR(128) NOT NULL,
    origin VARCHAR(32) NOT NULL CHECK (origin IN ('k8s_top', 'exporter')),
    metric VARCHAR(32) NOT NULL CHECK (metric IN ('cpu', 'memory', 'gpu', 'network', 'requests', 'storage', 'storage_total', 'storage_available')),
    device_id VARCHAR(64) NOT NULL DEFAULT '',
    CHECK ((metric = 'gpu' AND CHAR_LENGTH(device_id) = 40 AND SUBSTRING(device_id, 1, 4) = 'GPU-')
        OR (metric = 'network' AND CHAR_LENGTH(device_id) BETWEEN 1 AND 15 AND device_id REGEXP '^[a-zA-Z0-9][a-zA-Z0-9_.-]*$')
        OR (metric IN ('storage', 'storage_total', 'storage_available') AND SUBSTRING(device_id, 1, 1) = '/')
        OR (metric IN ('cpu', 'memory', 'requests', 'storage', 'network') AND device_id = '')),
    UNIQUE KEY asset_monitor_series_identity (cluster, instance, type, asset_id, namespace, origin, metric, device_id)
) ENGINE=InnoDB;

CREATE TABLE asset_monitor_rules (
    series_id BIGINT PRIMARY KEY AUTO_INCREMENT,
    threshold DOUBLE NOT NULL CHECK (threshold >= 0),
    duration_seconds BIGINT NOT NULL CHECK (duration_seconds BETWEEN 1 AND 604800),
    effective_ms BIGINT NOT NULL,
    first_above_ms BIGINT,
    FOREIGN KEY (series_id) REFERENCES asset_monitor_series(id)
) ENGINE=InnoDB;

CREATE TABLE asset_monitor_samples (
    series_id BIGINT NOT NULL,
    at_ms BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL CHECK (status IN ('ok', 'missing', 'not_applicable')),
    value DOUBLE,
    PRIMARY KEY (series_id, at_ms),
    FOREIGN KEY (series_id) REFERENCES asset_monitor_series(id),
    CHECK ((status = 'ok' AND value IS NOT NULL AND value >= 0) OR (status <> 'ok' AND value IS NULL))
) ENGINE=InnoDB;

CREATE TABLE asset_monitor_alerts (
    series_id BIGINT NOT NULL,
    opened_ms BIGINT NOT NULL,
    threshold DOUBLE NOT NULL,
    duration_seconds BIGINT NOT NULL,
    ended_ms BIGINT,
    state VARCHAR(32) NOT NULL CHECK (state IN ('open', 'recovered', 'interrupted', 'reconfigured')),
    PRIMARY KEY (series_id, opened_ms),
    FOREIGN KEY (series_id) REFERENCES asset_monitor_series(id)
) ENGINE=InnoDB;

CREATE TABLE tokens (
    id VARCHAR(64) PRIMARY KEY,
    kind VARCHAR(32) NOT NULL,
    user_id VARCHAR(64),
    name VARCHAR(128) NOT NULL,
    prefix VARCHAR(64) NOT NULL,
    hash VARCHAR(64) NOT NULL,
    cluster VARCHAR(64) NOT NULL DEFAULT '',
    instance VARCHAR(64) NOT NULL DEFAULT '',
    expires_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    issued_via VARCHAR(32) NOT NULL DEFAULT '',
    FOREIGN KEY (user_id) REFERENCES users(id),
    KEY idx_tokens_kind (kind)
) ENGINE=InnoDB;

CREATE TABLE grants (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id VARCHAR(64) NOT NULL,
    cluster VARCHAR(64) NOT NULL,
    instance VARCHAR(64) NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE KEY grants_user_target (user_id, cluster, instance),
    FOREIGN KEY (user_id) REFERENCES users(id),
    KEY idx_grants_user (user_id)
) ENGINE=InnoDB;

CREATE TABLE credentials (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    scope VARCHAR(64) NOT NULL,
    owner_id VARCHAR(64) NOT NULL DEFAULT '',
    type VARCHAR(32) NOT NULL,
    state VARCHAR(32) NOT NULL DEFAULT 'active',
    created_by VARCHAR(64) NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL,
    UNIQUE KEY credentials_scope_owner_name (scope, owner_id, name),
    FOREIGN KEY (owner_id) REFERENCES users(id),
    FOREIGN KEY (created_by) REFERENCES users(id),
    KEY idx_credentials_name (name, state),
    KEY idx_credentials_owner (owner_id, state, created_at(191) DESC)
) ENGINE=InnoDB;

CREATE TABLE credential_versions (
    id VARCHAR(64) PRIMARY KEY,
    credential_id VARCHAR(64) NOT NULL,
    payload_ciphertext TEXT NOT NULL,
    payload_digest VARCHAR(191) NOT NULL,
    state VARCHAR(32) NOT NULL,
    created_by VARCHAR(64) NOT NULL,
    created_at TEXT NOT NULL,
    promoted_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials(id),
    FOREIGN KEY (created_by) REFERENCES users(id),
    KEY idx_credential_versions_resource (credential_id, created_at(191) DESC)
) ENGINE=InnoDB;

CREATE TABLE credential_grants (
    id VARCHAR(64) PRIMARY KEY,
    credential_id VARCHAR(64) NOT NULL,
    grantee_id VARCHAR(64) NOT NULL,
    scope_json TEXT NOT NULL,
    state VARCHAR(32) NOT NULL DEFAULT 'active',
    created_by VARCHAR(64) NOT NULL,
    created_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials(id),
    FOREIGN KEY (grantee_id) REFERENCES users(id),
    FOREIGN KEY (created_by) REFERENCES users(id),
    KEY idx_credential_grants_authorize (grantee_id, state, credential_id)
) ENGINE=InnoDB;

CREATE TABLE credential_bundles (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    scope VARCHAR(64) NOT NULL DEFAULT 'personal',
    owner_id VARCHAR(64) NOT NULL DEFAULT '',
    state VARCHAR(32) NOT NULL DEFAULT 'active',
    created_by VARCHAR(64) NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE KEY credential_bundles_owner_name (owner_id, name),
    FOREIGN KEY (owner_id) REFERENCES users(id),
    FOREIGN KEY (created_by) REFERENCES users(id)
) ENGINE=InnoDB;

CREATE TABLE credential_bundle_items (
    bundle_id VARCHAR(64) NOT NULL,
    credential_id VARCHAR(64) NOT NULL,
    PRIMARY KEY (bundle_id, credential_id),
    FOREIGN KEY (bundle_id) REFERENCES credential_bundles(id),
    FOREIGN KEY (credential_id) REFERENCES credentials(id),
    KEY idx_credential_bundle_items_resource (credential_id)
) ENGINE=InnoDB;

CREATE TABLE credential_materializations (
    id VARCHAR(64) PRIMARY KEY,
    credential_id VARCHAR(64) NOT NULL,
    version_id VARCHAR(64) NOT NULL,
    grant_id VARCHAR(64) NOT NULL,
    `use` VARCHAR(64) NOT NULL,
    context_key VARCHAR(128) NOT NULL,
    request_json TEXT NOT NULL,
    evidence_json TEXT NOT NULL,
    status VARCHAR(32) NOT NULL,
    error_category VARCHAR(191) NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials(id),
    FOREIGN KEY (version_id) REFERENCES credential_versions(id),
    FOREIGN KEY (grant_id) REFERENCES credential_grants(id),
    KEY idx_credential_materializations_resource (credential_id, version_id, grant_id, status)
) ENGINE=InnoDB;

CREATE TABLE credential_verifications (
    id VARCHAR(64) PRIMARY KEY,
    credential_id VARCHAR(64) NOT NULL,
    version_id VARCHAR(64) NOT NULL,
    grant_id VARCHAR(64) NOT NULL,
    `use` VARCHAR(64) NOT NULL,
    context_key VARCHAR(128) NOT NULL,
    request_json TEXT NOT NULL,
    evidence_json TEXT NOT NULL,
    status VARCHAR(32) NOT NULL,
    verified_at TEXT NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials(id),
    FOREIGN KEY (version_id) REFERENCES credential_versions(id),
    FOREIGN KEY (grant_id) REFERENCES credential_grants(id),
    KEY idx_credential_verifications_resource (credential_id, version_id, status)
) ENGINE=InnoDB;

CREATE TABLE inventory_machine_login_accounts (
    asset_id VARCHAR(128) NOT NULL,
    username VARCHAR(128) NOT NULL,
    credential_id VARCHAR(64) NOT NULL,
    version_id VARCHAR(64) NOT NULL,
    PRIMARY KEY (asset_id, username),
    FOREIGN KEY (asset_id) REFERENCES inventory_machines(asset_id),
    FOREIGN KEY (credential_id) REFERENCES credentials(id),
    FOREIGN KEY (version_id) REFERENCES credential_versions(id),
    UNIQUE KEY inventory_machine_login_credential (credential_id),
    UNIQUE KEY inventory_machine_login_version (version_id)
) ENGINE=InnoDB;

CREATE TABLE inventory_machine_login_key (
    singleton BIGINT PRIMARY KEY AUTO_INCREMENT,
    key_mac VARCHAR(191) NOT NULL
) ENGINE=InnoDB;

-- ===========================================================================
-- Triggers
--
-- MySQL enforces foreign keys natively, which covers most of what the SQLite
-- triggers compensated for. The four node-link guard triggers are kept anyway
-- (fail-closed parity with SQLite; the FK alone is equivalent for these
-- cases). Two behavior notes:
--   * machine_diagnostic_bindings/samples now cascade via the native FK on
--     inventory_machines instead of trigger bodies. Deleting a machine still
--     removes diagnostics; re-targeting a machine does not (the SQLite
--     trigger did), because a MySQL trigger cannot portably distinguish the
--     two without application help. The application already deletes
--     diagnostics explicitly before re-targeting (see asset_monitor_store).
--   * inspection_cycle: the SQLite partial UNIQUE INDEX on
--     inspection_runs(plan_id, due) WHERE trigger_kind='manual' becomes
--     BEFORE INSERT/UPDATE enforcer triggers, because MySQL cannot express a
--     conditional unique index.
-- ===========================================================================

DELIMITER ;;

CREATE TRIGGER asset_runtime_deployment_drop AFTER DELETE ON inventory_business_deployments
FOR EACH ROW
BEGIN
    IF NOT EXISTS (SELECT 1 FROM inventory_business_deployments
        WHERE cluster = OLD.cluster AND instance = OLD.instance
          AND namespace = OLD.namespace AND application = OLD.application
          AND verified_at <> '') THEN
        DELETE FROM asset_runtime_bindings
            WHERE cluster = OLD.cluster AND instance = OLD.instance
              AND namespace = OLD.namespace AND application = OLD.application;
    END IF;
END;;

CREATE TRIGGER asset_runtime_ns_drop AFTER DELETE ON cluster_inventory_namespaces
FOR EACH ROW
BEGIN
    DELETE FROM asset_runtime_snapshots
        WHERE cluster = OLD.cluster AND instance = OLD.instance AND namespace = OLD.name;
    DELETE FROM asset_runtime_bindings
        WHERE cluster = OLD.cluster AND instance = OLD.instance AND namespace = OLD.name;
END;;

CREATE TRIGGER asset_runtime_ns_recreated AFTER UPDATE ON cluster_inventory_namespaces
FOR EACH ROW
BEGIN
    IF OLD.uid <> '' AND OLD.uid <> NEW.uid THEN
        DELETE FROM asset_runtime_snapshots
            WHERE cluster = OLD.cluster AND instance = OLD.instance AND namespace = OLD.name;
        DELETE FROM asset_runtime_bindings
            WHERE cluster = OLD.cluster AND instance = OLD.instance AND namespace = OLD.name;
    END IF;
END;;

CREATE TRIGGER asset_runtime_source_drop AFTER DELETE ON cluster_inventory_sources
FOR EACH ROW
BEGIN
    DELETE FROM asset_runtime_snapshots
        WHERE cluster = OLD.cluster AND instance = OLD.instance;
    DELETE FROM asset_runtime_bindings
        WHERE cluster = OLD.cluster AND instance = OLD.instance;
END;;

CREATE TRIGGER inventory_node_links_check_insert BEFORE INSERT ON inventory_node_links
FOR EACH ROW
BEGIN
    IF NOT EXISTS (SELECT 1 FROM inventory_machines WHERE asset_id = NEW.machine_id)
        OR NOT EXISTS (SELECT 1 FROM cluster_inventory_sources
            WHERE cluster = NEW.cluster AND instance = NEW.instance) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'missing node link parent';
    END IF;
END;;

CREATE TRIGGER inventory_node_links_check_update BEFORE UPDATE ON inventory_node_links
FOR EACH ROW
BEGIN
    IF NOT EXISTS (SELECT 1 FROM inventory_machines WHERE asset_id = NEW.machine_id)
        OR NOT EXISTS (SELECT 1 FROM cluster_inventory_sources
            WHERE cluster = NEW.cluster AND instance = NEW.instance) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'missing node link parent';
    END IF;
END;;

CREATE TRIGGER inventory_node_links_machine_delete BEFORE DELETE ON inventory_machines
FOR EACH ROW
BEGIN
    IF EXISTS (SELECT 1 FROM inventory_node_links WHERE machine_id = OLD.asset_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'machine has node links';
    END IF;
END;;

CREATE TRIGGER inventory_node_links_machine_rekey BEFORE UPDATE ON inventory_machines
FOR EACH ROW
BEGIN
    IF NEW.asset_id <> OLD.asset_id
        AND EXISTS (SELECT 1 FROM inventory_node_links WHERE machine_id = OLD.asset_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'machine has node links';
    END IF;
END;;

CREATE TRIGGER inventory_node_links_source_delete BEFORE DELETE ON cluster_inventory_sources
FOR EACH ROW
BEGIN
    IF EXISTS (SELECT 1 FROM inventory_node_links
        WHERE cluster = OLD.cluster AND instance = OLD.instance) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'cluster source has node links';
    END IF;
END;;

CREATE TRIGGER inventory_node_links_source_rekey BEFORE UPDATE ON cluster_inventory_sources
FOR EACH ROW
BEGIN
    IF (NEW.asset_id <> OLD.asset_id OR NEW.cluster <> OLD.cluster OR NEW.instance <> OLD.instance)
        AND EXISTS (SELECT 1 FROM inventory_node_links
            WHERE cluster = OLD.cluster AND instance = OLD.instance) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'cluster source has node links';
    END IF;
END;;

CREATE TRIGGER machine_diagnostic_asset_deleted BEFORE DELETE ON inventory_machines
FOR EACH ROW
BEGIN
    DELETE FROM machine_diagnostic_samples WHERE asset_id = OLD.asset_id;
    DELETE FROM machine_diagnostic_bindings WHERE asset_id = OLD.asset_id;
END;;

-- Replaces SQLite's partial UNIQUE INDEX on asset_monitor_alerts(series_id)
-- WHERE state='open'. Stricter than SQLite: any second open row is rejected.
CREATE TRIGGER asset_monitor_one_open_insert BEFORE INSERT ON asset_monitor_alerts
FOR EACH ROW
BEGIN
    IF NEW.state = 'open' AND EXISTS (SELECT 1 FROM asset_monitor_alerts
        WHERE series_id = NEW.series_id AND state = 'open'
          AND NOT (series_id = NEW.series_id AND opened_ms = NEW.opened_ms)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'one open alert per series';
    END IF;
END;;

CREATE TRIGGER asset_monitor_one_open_update BEFORE UPDATE ON asset_monitor_alerts
FOR EACH ROW
BEGIN
    IF NEW.state = 'open' AND EXISTS (SELECT 1 FROM asset_monitor_alerts
        WHERE series_id = NEW.series_id AND state = 'open'
          AND NOT (series_id = NEW.series_id AND opened_ms = NEW.opened_ms)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'one open alert per series';
    END IF;
END;;

-- Replaces SQLite's expression UNIQUE INDEX on inspection_runs(plan_id, due,
-- (trigger_kind='manual')), which made the triple unique only among manual
-- runs. A plain MySQL index cannot express that; enforce it with triggers.
CREATE TRIGGER inspection_cycle_insert BEFORE INSERT ON inspection_runs
FOR EACH ROW
BEGIN
    IF NEW.trigger_kind = 'manual' AND EXISTS (SELECT 1 FROM inspection_runs
        WHERE plan_id = NEW.plan_id AND due = NEW.due AND trigger_kind = 'manual'
          AND id <> NEW.id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'duplicate manual inspection cycle';
    END IF;
END;;

CREATE TRIGGER inspection_cycle_update BEFORE UPDATE ON inspection_runs
FOR EACH ROW
BEGIN
    IF NEW.trigger_kind = 'manual' AND EXISTS (SELECT 1 FROM inspection_runs
        WHERE plan_id = NEW.plan_id AND due = NEW.due AND trigger_kind = 'manual'
          AND id <> NEW.id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'duplicate manual inspection cycle';
    END IF;
END;;


-- MySQL forbids a CHECK constraint on an AUTO_INCREMENT column, so the
-- SQLite singleton CHECK (x = 1) on these three tables is enforced by a
-- trigger. The copier inserts the existing row id explicitly, so the
-- generator never advances past 1.
CREATE TRIGGER edge_route_revision_singleton BEFORE INSERT ON edge_route_revision
FOR EACH ROW
BEGIN
    IF NEW.id <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'edge_route_revision is a singleton';
    END IF;
END;;

CREATE TRIGGER gateway_secret_key_check_singleton BEFORE INSERT ON gateway_secret_key_check
FOR EACH ROW
BEGIN
    IF NEW.singleton <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'gateway_secret_key_check is a singleton';
    END IF;
END;;

CREATE TRIGGER inventory_machine_login_key_singleton BEFORE INSERT ON inventory_machine_login_key
FOR EACH ROW
BEGIN
    IF NEW.singleton <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'inventory_machine_login_key is a singleton';
    END IF;
END;;

DELIMITER ;
