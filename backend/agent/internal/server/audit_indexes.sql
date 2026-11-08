CREATE INDEX IF NOT EXISTS idx_audit_user_id ON audit_events(user_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_user_session_id ON audit_events(user_id, session, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_target_id ON audit_events(cluster, instance, id DESC);
-- Status and action filters. The trailing column lets a query that filters on
-- both skip non-matching rows inside the index. With both filters SQLite rates
-- the two indexes equally and takes the one created last, so the action
-- indexes, usually the narrower filter, come after the status indexes.
CREATE INDEX IF NOT EXISTS idx_audit_status_id ON audit_events(status, id DESC, action);
CREATE INDEX IF NOT EXISTS idx_audit_user_status_id ON audit_events(user_id, status, id DESC, action);
CREATE INDEX IF NOT EXISTS idx_audit_target_status_id ON audit_events(cluster, instance, status, id DESC, action);
CREATE INDEX IF NOT EXISTS idx_audit_action_id ON audit_events(action, id DESC, status);
CREATE INDEX IF NOT EXISTS idx_audit_user_action_id ON audit_events(user_id, action, id DESC, status);
CREATE INDEX IF NOT EXISTS idx_audit_target_action_id ON audit_events(cluster, instance, action, id DESC, status);
-- One session within a target. Created after the target action/status indexes
-- so a session filter wins over them.
CREATE INDEX IF NOT EXISTS idx_audit_session_target_id ON audit_events(session, cluster, instance, id DESC);
-- A session on its own: credential resource audits store the credential id as
-- the session and leave the target empty. Without it a session plus status
-- filter would walk the status index instead.
CREATE INDEX IF NOT EXISTS idx_audit_session_id ON audit_events(session, id DESC);
