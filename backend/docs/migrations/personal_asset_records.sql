-- Apply to the Gateway SQLite database before enabling personal asset registration.
CREATE TABLE IF NOT EXISTS personal_asset_records (
    owner TEXT NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    name TEXT NOT NULL,
    environment TEXT NOT NULL,
    revision INTEGER NOT NULL,
    PRIMARY KEY(owner, kind, asset_id)
);
