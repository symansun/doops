-- Upgrade a legacy SQLite asset_monitor_series table to the current CHECK
-- constraint set (device dimensions, disk I/O, container/app/agent_host
-- origins, and database metrics) while preserving series IDs and child rows in
-- asset_monitor_samples / asset_monitor_rules / asset_monitor_alerts.
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;
CREATE TABLE asset_monitor_series_new (
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
INSERT INTO asset_monitor_series_new(id,cluster,instance,namespace,type,asset_id,origin,metric,device_id)
 SELECT id,cluster,instance,namespace,type,asset_id,origin,metric,device_id FROM asset_monitor_series;
DROP TABLE asset_monitor_series;
ALTER TABLE asset_monitor_series_new RENAME TO asset_monitor_series;
CREATE TEMP TABLE monitor_fk_check (violations INTEGER CHECK(violations=0));
INSERT INTO monitor_fk_check SELECT count(*) FROM pragma_foreign_key_check WHERE parent='asset_monitor_series';
DROP TABLE monitor_fk_check;
COMMIT;
PRAGMA foreign_keys=ON;
