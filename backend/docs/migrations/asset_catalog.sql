-- Apply after the platform inventory migrations. Existing private notes stay private.
BEGIN IMMEDIATE;
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
COMMIT;
