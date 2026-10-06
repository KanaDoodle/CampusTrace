CREATE TABLE IF NOT EXISTS source_import_batches (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, created_at DATETIME(6) NOT NULL,
 INDEX owner_import(user_id,created_at,id), FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS source_import_items (
 id VARCHAR(32) PRIMARY KEY, batch_id VARCHAR(32) NOT NULL, position INT NOT NULL,
 watch_id VARCHAR(32) NULL, body JSON NOT NULL,
 UNIQUE batch_position(batch_id,position), INDEX import_watch(watch_id),
 FOREIGN KEY(batch_id) REFERENCES source_import_batches(id) ON DELETE CASCADE,
 FOREIGN KEY(watch_id) REFERENCES watch_targets(id) ON DELETE SET NULL
);
INSERT IGNORE INTO schema_migrations(version) VALUES('source-import-v1');
