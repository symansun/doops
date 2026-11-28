-- RES-01 / MySQL 8.4. This file is a read-only preflight for the Gateway database.
-- In the DoOps maintenance session, confirm the target and SELECT DATABASE(), stop
-- Gateway writers, and record SELECT count(*) FROM agent_status. Save the result
-- of this query. For UPGRADE, execute its single atomic InnoDB ALTER in the same
-- database. For STOP, inspect SHOW CREATE TABLE agent_status. READY needs no DDL.
-- Repeat after execution and after Gateway restart: both results must be READY with
-- NULL upgrade_sql. Verify the row count and read
-- SELECT cluster, instance, mode, kubernetes_available FROM agent_status ORDER BY cluster, instance;
-- Existing values survive; newly added columns read '' and NULL until an Agent reports.
-- Compare a known offline instance with the authenticated admin response from
-- GET /v1/admin/instances?include=history (unknown capabilities are omitted).
WITH target_key AS (
    SELECT group_concat(column_name ORDER BY ordinal_position) AS pk
    FROM information_schema.key_column_usage
    WHERE table_schema = database() AND table_name = 'agent_status' AND constraint_name = 'PRIMARY'
), layout AS (
    SELECT max(CASE WHEN column_name = 'mode' THEN
               if(column_type = 'varchar(16)' AND is_nullable = 'NO' AND octet_length(column_default) = 0 AND extra = '', 1, 0)
           END) AS mode_ok,
           max(CASE WHEN column_name = 'kubernetes_available' THEN
               if(column_type = 'tinyint(1)' AND is_nullable = 'YES' AND column_default IS NULL AND extra = '', 1, 0)
           END) AS kube_ok
    FROM information_schema.columns
    WHERE table_schema = database() AND table_name = 'agent_status'
), plan AS (
    SELECT layout.*, CASE
        WHEN coalesce(pk, '') <> 'cluster,instance' THEN 'STOP: expected agent_status primary key (cluster, instance)'
        WHEN mode_ok = 0 THEN 'STOP: unexpected agent_status.mode definition'
        WHEN kube_ok = 0 THEN 'STOP: unexpected agent_status.kubernetes_available definition'
        WHEN mode_ok = 1 AND kube_ok = 1 THEN 'READY'
        ELSE 'UPGRADE'
    END AS status FROM layout CROSS JOIN target_key
)
SELECT status, CASE WHEN status = 'UPGRADE' THEN concat('ALTER TABLE agent_status ', concat_ws(', ',
    if(mode_ok IS NULL, 'ADD COLUMN mode VARCHAR(16) NOT NULL DEFAULT ''''', NULL),
    if(kube_ok IS NULL, 'ADD COLUMN kubernetes_available TINYINT(1) NULL', NULL)), ';') END AS upgrade_sql
FROM plan;
