-- Gateway SQLite. Apply after the inventory and agent asset migrations.
BEGIN IMMEDIATE;

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

COMMIT;
