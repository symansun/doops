-- RES-01 / SQLite. This file is a read-only preflight for the Gateway database.
-- In the DoOps maintenance session, confirm the target and the main file (PRAGMA
-- database_list), stop Gateway writers, and record SELECT count(*) FROM agent_status.
-- Save this result. For UPGRADE, execute the returned upgrade_sql on one connection
-- with stop-on-error; roll back/disconnect on error. For STOP, inspect the layout.
-- READY requires no DDL. Repeat this query after execution and after Gateway restart:
-- both results must be READY with NULL upgrade_sql. Verify the row count and read
-- SELECT cluster, instance, mode, kubernetes_available FROM agent_status ORDER BY cluster, instance;
-- Existing values survive; newly added columns read '' and NULL until an Agent reports.
-- Compare a known offline instance with the authenticated admin response from
-- GET /v1/admin/instances?include=history (unknown capabilities are omitted).
WITH layout AS (
    SELECT count(*) FILTER (WHERE pk > 0) = 2 AND
           count(*) FILTER (WHERE (name = 'cluster' AND pk = 1) OR (name = 'instance' AND pk = 2)) = 2 AS gateway_key,
           max(CASE WHEN lower(name) = 'mode' THEN
               iif(upper(type) = 'TEXT' AND "notnull" = 1 AND dflt_value = quote('') AND hidden = 0, 1, 0)
           END) AS mode_ok,
           max(CASE WHEN lower(name) = 'kubernetes_available' THEN
               iif(upper(type) = 'INTEGER' AND "notnull" = 0 AND coalesce(upper(dflt_value), 'NULL') = 'NULL' AND hidden = 0, 1, 0)
           END) AS kube_ok
    FROM pragma_table_xinfo('agent_status', 'main')
), plan AS (
    SELECT *, CASE
        WHEN NOT gateway_key THEN 'STOP: expected agent_status primary key (cluster, instance)'
        WHEN mode_ok = 0 THEN 'STOP: unexpected agent_status.mode definition'
        WHEN kube_ok = 0 THEN 'STOP: unexpected agent_status.kubernetes_available definition'
        WHEN mode_ok = 1 AND kube_ok = 1 THEN 'READY'
        ELSE 'UPGRADE'
    END AS status FROM layout
)
SELECT status, CASE WHEN status = 'UPGRADE' THEN 'BEGIN IMMEDIATE; ' ||
    CASE WHEN mode_ok IS NULL THEN 'ALTER TABLE main.agent_status ADD COLUMN mode TEXT NOT NULL DEFAULT ''''; ' ELSE '' END ||
    CASE WHEN kube_ok IS NULL THEN 'ALTER TABLE main.agent_status ADD COLUMN kubernetes_available INTEGER; ' ELSE '' END ||
    'COMMIT;' END AS upgrade_sql
FROM plan;
