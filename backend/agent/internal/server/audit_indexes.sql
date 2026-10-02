CREATE INDEX IF NOT EXISTS idx_audit_user_id ON audit_events(user_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_user_session_id ON audit_events(user_id, session, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_target_id ON audit_events(cluster, instance, id DESC);
