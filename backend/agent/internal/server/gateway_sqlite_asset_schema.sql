-- Authoritative SQLite DDL for all asset, inventory, monitoring, inspection,
-- diagnostics, catalog, and target-resource-sync tables.
-- Applied automatically and idempotently on GatewayStore startup (SQLite) so
-- fresh and upgraded deployments match MySQL startup parity with zero manual SQL.
-- Matches backend/docs/migrations/*.sql in dependency order.

-- 1. machine_inventory.sql (+ inventory_machines.active from asset_coverage.sql)
CREATE TABLE IF NOT EXISTS inventory_machines (
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
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_at TEXT NOT NULL DEFAULT '',
    CHECK (host_asset_id IS NULL OR (kind = 'vm' AND host_asset_id <> asset_id)),
    CHECK ((cluster = '') = (instance = ''))
) STRICT;
CREATE INDEX IF NOT EXISTS inventory_machines_target ON inventory_machines(cluster, instance);
CREATE INDEX IF NOT EXISTS inventory_machines_host ON inventory_machines(host_asset_id) WHERE host_asset_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS inventory_import_attempts (
    attempt_id INTEGER PRIMARY KEY,
    batch_id TEXT NOT NULL,
    receipt TEXT NOT NULL
) STRICT;
CREATE INDEX IF NOT EXISTS inventory_import_attempts_batch ON inventory_import_attempts(batch_id, attempt_id);

CREATE TABLE IF NOT EXISTS inventory_machine_login_key (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    key_mac TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS inventory_machine_login_accounts (
    asset_id TEXT NOT NULL REFERENCES inventory_machines(asset_id),
    username TEXT NOT NULL,
    credential_id TEXT NOT NULL UNIQUE REFERENCES credentials(id),
    version_id TEXT NOT NULL UNIQUE REFERENCES credential_versions(id),
    PRIMARY KEY (asset_id, username)
) STRICT;

CREATE TABLE IF NOT EXISTS inventory_machine_tags (
    asset_id TEXT NOT NULL REFERENCES inventory_machines(asset_id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY (asset_id, tag)
) STRICT;
CREATE INDEX IF NOT EXISTS inventory_machine_tags_by_tag ON inventory_machine_tags(tag, asset_id);

-- 2. cluster_inventory.sql
CREATE TABLE IF NOT EXISTS cluster_inventory_sources (
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
CREATE INDEX IF NOT EXISTS cluster_inventory_asset_id ON cluster_inventory_sources(asset_id);

CREATE TABLE IF NOT EXISTS cluster_inventory_namespaces (
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

-- 3. inventory_node_links.sql
CREATE TABLE IF NOT EXISTS inventory_node_links (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    node_name TEXT NOT NULL CHECK (length(node_name) BETWEEN 1 AND 253),
    machine_id TEXT NOT NULL REFERENCES inventory_machines(asset_id),
    PRIMARY KEY (cluster, instance, node_name),
    FOREIGN KEY (cluster, instance) REFERENCES cluster_inventory_sources(cluster, instance)
) STRICT;
CREATE INDEX IF NOT EXISTS inventory_node_links_machine ON inventory_node_links(machine_id);

CREATE TRIGGER IF NOT EXISTS inventory_node_links_check_insert BEFORE INSERT ON inventory_node_links
WHEN NOT EXISTS (SELECT 1 FROM inventory_machines WHERE asset_id = NEW.machine_id)
  OR NOT EXISTS (SELECT 1 FROM cluster_inventory_sources WHERE (cluster, instance) = (NEW.cluster, NEW.instance))
BEGIN SELECT RAISE(ABORT, 'missing node link parent'); END;
CREATE TRIGGER IF NOT EXISTS inventory_node_links_check_update BEFORE UPDATE OF machine_id, cluster, instance ON inventory_node_links
WHEN NOT EXISTS (SELECT 1 FROM inventory_machines WHERE asset_id = NEW.machine_id)
  OR NOT EXISTS (SELECT 1 FROM cluster_inventory_sources WHERE (cluster, instance) = (NEW.cluster, NEW.instance))
BEGIN SELECT RAISE(ABORT, 'missing node link parent'); END;
CREATE TRIGGER IF NOT EXISTS inventory_node_links_machine_delete BEFORE DELETE ON inventory_machines
WHEN EXISTS (SELECT 1 FROM inventory_node_links WHERE machine_id = OLD.asset_id)
BEGIN SELECT RAISE(ABORT, 'machine has node links'); END;
CREATE TRIGGER IF NOT EXISTS inventory_node_links_machine_rekey BEFORE UPDATE OF asset_id ON inventory_machines
WHEN NEW.asset_id <> OLD.asset_id AND EXISTS (SELECT 1 FROM inventory_node_links WHERE machine_id = OLD.asset_id)
BEGIN SELECT RAISE(ABORT, 'machine has node links'); END;
CREATE TRIGGER IF NOT EXISTS inventory_node_links_source_delete BEFORE DELETE ON cluster_inventory_sources
WHEN EXISTS (SELECT 1 FROM inventory_node_links WHERE (cluster, instance) = (OLD.cluster, OLD.instance))
BEGIN SELECT RAISE(ABORT, 'cluster source has node links'); END;
CREATE TRIGGER IF NOT EXISTS inventory_node_links_source_rekey BEFORE UPDATE OF asset_id, cluster, instance ON cluster_inventory_sources
WHEN (NEW.asset_id, NEW.cluster, NEW.instance) <> (OLD.asset_id, OLD.cluster, OLD.instance)
 AND EXISTS (SELECT 1 FROM inventory_node_links WHERE (cluster, instance) = (OLD.cluster, OLD.instance))
BEGIN SELECT RAISE(ABORT, 'cluster source has node links'); END;

-- 4. business_inventory.sql
CREATE TABLE IF NOT EXISTS inventory_business_systems (
    asset_id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    owner TEXT NOT NULL,
    active INTEGER NOT NULL CHECK (active IN (0,1)),
    revision INTEGER NOT NULL CHECK (revision > 0)
);
CREATE TABLE IF NOT EXISTS inventory_business_deployments (
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
CREATE INDEX IF NOT EXISTS inventory_business_deployment_target
    ON inventory_business_deployments(cluster, instance, asset_id);

-- 5. asset_coverage.sql (baselines table; inventory_machines.active also ensured via ensureColumn)
CREATE TABLE IF NOT EXISTS asset_coverage_baselines (
    id TEXT PRIMARY KEY NOT NULL,
    frozen_at TEXT NOT NULL,
    frozen_by TEXT NOT NULL,
    snapshot TEXT NOT NULL CHECK (json_valid(snapshot))
) STRICT;

-- 6. asset_monitor.sql
CREATE TABLE IF NOT EXISTS asset_monitor_series (
 id INTEGER PRIMARY KEY, cluster TEXT NOT NULL, instance TEXT NOT NULL, namespace TEXT NOT NULL,
 type TEXT NOT NULL CHECK(type IN ('machine','os','kernel','process','container','app','database')),
 asset_id TEXT NOT NULL, origin TEXT NOT NULL CHECK(origin IN ('k8s_top','agent_host','container_stats','app_probe','exporter')),
 metric TEXT NOT NULL CHECK(metric IN ('cpu','memory','memory_limit','gpu','network','requests','response_time_ms','active_connections','error_rate','db_tps','db_lock_waits','storage','storage_total','storage_available',
    'disk_read_iops','disk_write_iops','disk_read_bytes_per_second','disk_write_bytes_per_second','disk_read_latency_ms','disk_write_latency_ms')),
 device_id TEXT NOT NULL DEFAULT '',
 CHECK((metric='gpu' AND length(device_id)=40 AND substr(device_id,1,4)='GPU-')
    OR (metric='network' AND length(device_id) BETWEEN 1 AND 15 AND device_id GLOB '[a-zA-Z0-9]*' AND device_id NOT GLOB '*[^a-zA-Z0-9_.-]*')
    OR (metric IN ('storage','storage_total','storage_available') AND substr(device_id,1,1)='/')
    OR (metric GLOB 'disk_*' AND type='machine' AND namespace='' AND origin IN ('exporter','agent_host')
        AND length(device_id) BETWEEN 1 AND 63 AND device_id GLOB '[a-zA-Z0-9]*' AND device_id NOT GLOB '*[^a-zA-Z0-9_.-]*')
    OR (metric='memory_limit' AND type='container' AND device_id='')
    OR (metric GLOB 'db_*' AND type='database' AND device_id='')
    OR (metric IN ('cpu','memory','requests','response_time_ms','active_connections','error_rate','storage','network') AND device_id='')),
 UNIQUE(cluster,instance,type,asset_id,namespace,origin,metric,device_id)
);
CREATE TABLE IF NOT EXISTS asset_monitor_samples (
 series_id INTEGER NOT NULL REFERENCES asset_monitor_series(id), at_ms INTEGER NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('ok','missing','not_applicable')), value REAL,
 CHECK((status='ok' AND value IS NOT NULL AND value>=0) OR (status<>'ok' AND value IS NULL)),
 PRIMARY KEY(series_id,at_ms)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS asset_monitor_rules (
 series_id INTEGER PRIMARY KEY REFERENCES asset_monitor_series(id), threshold REAL NOT NULL CHECK(threshold>=0),
 duration_seconds INTEGER NOT NULL CHECK(duration_seconds BETWEEN 1 AND 604800), effective_ms INTEGER NOT NULL,
 first_above_ms INTEGER
);
CREATE TABLE IF NOT EXISTS asset_monitor_alerts (
 series_id INTEGER NOT NULL REFERENCES asset_monitor_series(id), opened_ms INTEGER NOT NULL,
 threshold REAL NOT NULL, duration_seconds INTEGER NOT NULL, ended_ms INTEGER,
 state TEXT NOT NULL CHECK(state IN ('open','recovered','interrupted','reconfigured')),
 PRIMARY KEY(series_id,opened_ms)
) WITHOUT ROWID;
CREATE UNIQUE INDEX IF NOT EXISTS asset_monitor_one_open ON asset_monitor_alerts(series_id) WHERE state='open';

-- 7. asset_inspection.sql
CREATE TABLE IF NOT EXISTS inspection_plans (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL, owner TEXT NOT NULL, executor TEXT NOT NULL REFERENCES users(id),
 timezone TEXT NOT NULL, weekday INTEGER NOT NULL CHECK(weekday BETWEEN 0 AND 6), clock TEXT NOT NULL,
 next_due INTEGER NOT NULL, active INTEGER NOT NULL DEFAULT 1
) STRICT;
CREATE INDEX IF NOT EXISTS inspection_schedule ON inspection_plans(active,next_due);
CREATE TABLE IF NOT EXISTS inspection_scope (
 plan_id INTEGER NOT NULL REFERENCES inspection_plans(id), asset_id TEXT NOT NULL, cluster TEXT NOT NULL,
 instance TEXT NOT NULL, cluster_id TEXT NOT NULL, check_id TEXT NOT NULL,
 PRIMARY KEY(plan_id,asset_id,cluster,instance,check_id)
) STRICT;
CREATE TABLE IF NOT EXISTS inspection_runs (
 id INTEGER PRIMARY KEY, plan_id INTEGER NOT NULL REFERENCES inspection_plans(id), due INTEGER NOT NULL,
 frozen INTEGER NOT NULL, trigger_kind TEXT NOT NULL CHECK(trigger_kind IN ('manual','scheduled','reconciled'))
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS inspection_cycle ON inspection_runs(plan_id,due,(trigger_kind='manual'));
CREATE TABLE IF NOT EXISTS inspection_items (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES inspection_runs(id), asset_id TEXT NOT NULL,
 cluster TEXT NOT NULL, instance TEXT NOT NULL, cluster_id TEXT NOT NULL, check_id TEXT NOT NULL,
 generation INTEGER NOT NULL DEFAULT 1, opened INTEGER NOT NULL, deadline INTEGER NOT NULL,
 status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', repair TEXT NOT NULL DEFAULT '', repair_by TEXT NOT NULL DEFAULT '', repair_at INTEGER NOT NULL DEFAULT 0,
 UNIQUE(run_id,asset_id,cluster,instance,check_id)
) STRICT;
CREATE INDEX IF NOT EXISTS inspection_work ON inspection_items(status,deadline,id);
CREATE TABLE IF NOT EXISTS inspection_receipts (
 item_id INTEGER NOT NULL REFERENCES inspection_items(id), generation INTEGER NOT NULL, actor TEXT NOT NULL,
 received INTEGER NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL, evidence TEXT NOT NULL, digest TEXT NOT NULL,
 PRIMARY KEY(item_id,generation)
) STRICT;

-- 8. asset_coverage_registers.sql
CREATE TABLE IF NOT EXISTS asset_coverage_registers (
    scope TEXT PRIMARY KEY NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    document_ref TEXT NOT NULL,
    entries TEXT NOT NULL CHECK(json_valid(entries)),
    updated_by TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS asset_coverage_cluster_versions (
    cluster_id TEXT NOT NULL,
    id TEXT NOT NULL,
    frozen_at TEXT NOT NULL,
    frozen_by TEXT NOT NULL,
    snapshot TEXT NOT NULL CHECK(json_valid(snapshot)),
    document_ref TEXT NOT NULL,
    register_revision INTEGER NOT NULL,
    PRIMARY KEY(cluster_id,id)
) STRICT;

-- 9. asset_runtime_topology.sql
CREATE TABLE IF NOT EXISTS asset_runtime_snapshots (
 cluster TEXT NOT NULL, instance TEXT NOT NULL, namespace TEXT NOT NULL, asset_id TEXT NOT NULL,
 revision TEXT NOT NULL, observed_at TEXT NOT NULL, objects TEXT NOT NULL CHECK(json_valid(objects)),
 PRIMARY KEY(cluster,instance,namespace)
) STRICT;
CREATE TABLE IF NOT EXISTS asset_runtime_bindings (
 cluster TEXT NOT NULL, instance TEXT NOT NULL, namespace TEXT NOT NULL, application TEXT NOT NULL,
 workload_uid TEXT NOT NULL, verified_at TEXT NOT NULL,
 PRIMARY KEY(cluster,instance,namespace,application,workload_uid)
) STRICT;
CREATE TRIGGER IF NOT EXISTS asset_runtime_ns_drop AFTER DELETE ON cluster_inventory_namespaces BEGIN
 DELETE FROM asset_runtime_snapshots WHERE (cluster,instance,namespace)=(OLD.cluster,OLD.instance,OLD.name);
 DELETE FROM asset_runtime_bindings WHERE (cluster,instance,namespace)=(OLD.cluster,OLD.instance,OLD.name);
END;
CREATE TRIGGER IF NOT EXISTS asset_runtime_ns_recreated AFTER UPDATE OF uid ON cluster_inventory_namespaces WHEN OLD.uid<>'' AND OLD.uid<>NEW.uid BEGIN
 DELETE FROM asset_runtime_snapshots WHERE (cluster,instance,namespace)=(OLD.cluster,OLD.instance,OLD.name);
 DELETE FROM asset_runtime_bindings WHERE (cluster,instance,namespace)=(OLD.cluster,OLD.instance,OLD.name);
END;
CREATE TRIGGER IF NOT EXISTS asset_runtime_source_drop AFTER DELETE ON cluster_inventory_sources BEGIN
 DELETE FROM asset_runtime_snapshots WHERE (cluster,instance)=(OLD.cluster,OLD.instance);
 DELETE FROM asset_runtime_bindings WHERE (cluster,instance)=(OLD.cluster,OLD.instance);
END;
CREATE TRIGGER IF NOT EXISTS asset_runtime_deployment_drop AFTER DELETE ON inventory_business_deployments WHEN NOT EXISTS (
 SELECT 1 FROM inventory_business_deployments WHERE (cluster,instance,namespace,application)=(OLD.cluster,OLD.instance,OLD.namespace,OLD.application) AND verified_at<>'') BEGIN
 DELETE FROM asset_runtime_bindings WHERE (cluster,instance,namespace,application)=(OLD.cluster,OLD.instance,OLD.namespace,OLD.application);
END;

-- 10. machine_diagnostics.sql
CREATE TABLE IF NOT EXISTS machine_diagnostic_bindings (
    asset_id TEXT PRIMARY KEY NOT NULL REFERENCES inventory_machines(asset_id),
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    host_sha256 TEXT NOT NULL UNIQUE CHECK(length(host_sha256)=64 AND host_sha256 NOT GLOB '*[^0-9a-f]*')
);
CREATE TABLE IF NOT EXISTS machine_diagnostic_samples (
    asset_id TEXT NOT NULL REFERENCES machine_diagnostic_bindings(asset_id),
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    host_sha256 TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    generation INTEGER NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (asset_id, at_ms)
) STRICT;
CREATE TRIGGER IF NOT EXISTS machine_diagnostic_target_changed AFTER UPDATE OF cluster,instance ON inventory_machines
WHEN (OLD.cluster, OLD.instance) <> (NEW.cluster, NEW.instance)
BEGIN
    DELETE FROM machine_diagnostic_samples WHERE asset_id=OLD.asset_id;
    DELETE FROM machine_diagnostic_bindings WHERE asset_id=OLD.asset_id;
END;
CREATE TRIGGER IF NOT EXISTS machine_diagnostic_asset_deleted BEFORE DELETE ON inventory_machines
BEGIN
    DELETE FROM machine_diagnostic_samples WHERE asset_id=OLD.asset_id;
    DELETE FROM machine_diagnostic_bindings WHERE asset_id=OLD.asset_id;
END;

-- 11. agent_asset_scope.sql
CREATE TABLE IF NOT EXISTS inventory_agent_assets (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('machine','cluster','system')),
    asset_id TEXT NOT NULL,
    PRIMARY KEY(cluster,instance,kind,asset_id)
) STRICT;

-- 12. personal_asset_records.sql
CREATE TABLE IF NOT EXISTS personal_asset_records (
    owner TEXT NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    name TEXT NOT NULL,
    environment TEXT NOT NULL,
    revision INTEGER NOT NULL,
    PRIMARY KEY(owner, kind, asset_id)
);

-- 13. asset_catalog.sql
CREATE TABLE IF NOT EXISTS inventory_cluster_profiles (
 asset_id TEXT PRIMARY KEY, name TEXT NOT NULL, environment TEXT NOT NULL,
 contact TEXT NOT NULL, created_by TEXT NOT NULL, revision INTEGER NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS inventory_resource_catalog (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, cluster TEXT NOT NULL, instance TEXT NOT NULL,
 created_by TEXT NOT NULL, name TEXT NOT NULL, environment TEXT NOT NULL,
 details TEXT NOT NULL CHECK(json_valid(details)), identity_key TEXT NOT NULL, revision INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS catalog_bound_identity ON inventory_resource_catalog(kind,cluster,instance,identity_key) WHERE cluster<>'';
CREATE UNIQUE INDEX IF NOT EXISTS catalog_pending_identity ON inventory_resource_catalog(kind,created_by,identity_key) WHERE cluster='';

-- 14. agent_resource_sync.sql
CREATE TABLE IF NOT EXISTS agent_resource_state (
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
    PRIMARY KEY (cluster, instance),
    FOREIGN KEY (cluster, instance) REFERENCES agent_status(cluster, instance) ON DELETE CASCADE
) STRICT;
CREATE TABLE IF NOT EXISTS agent_observations (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('machine', 'cluster', 'namespace', 'container', 'site', 'database')),
    key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 128),
    details TEXT NOT NULL CHECK (json_valid(details) AND json_type(details) = 'object'),
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation >= 0),
    misses INTEGER NOT NULL DEFAULT 0 CHECK (misses >= 0),
    PRIMARY KEY (cluster, instance, kind, key),
    FOREIGN KEY (cluster, instance) REFERENCES agent_resource_state(cluster, instance) ON DELETE CASCADE
) STRICT;
CREATE INDEX IF NOT EXISTS idx_agent_observations_target
    ON agent_observations(cluster, instance, kind, key);
CREATE TABLE IF NOT EXISTS agent_resource_audit (
    audit_id INTEGER PRIMARY KEY REFERENCES audit_events(id) ON DELETE CASCADE,
    manifest_revision INTEGER NOT NULL CHECK (manifest_revision >= 0),
    resource_refs_json TEXT NOT NULL
        CHECK (json_valid(resource_refs_json) AND json_type(resource_refs_json) = 'array')
) STRICT;
