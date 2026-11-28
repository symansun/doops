-- Apply after machine, cluster and business inventory migrations.
BEGIN IMMEDIATE;
CREATE TABLE IF NOT EXISTS inventory_agent_assets (
    cluster TEXT NOT NULL,
    instance TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('machine','cluster','system')),
    asset_id TEXT NOT NULL,
    PRIMARY KEY(cluster,instance,kind,asset_id)
) STRICT;
COMMIT;
