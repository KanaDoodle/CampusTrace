CREATE TABLE IF NOT EXISTS search_rebuild_state (
 id VARCHAR(16) PRIMARY KEY,
 operation CHAR(32) NOT NULL DEFAULT '',
 dataset CHAR(32) NOT NULL DEFAULT '',
 source_generation VARCHAR(160) NOT NULL DEFAULT '',
 target_generation VARCHAR(160) NOT NULL DEFAULT '',
 phase VARCHAR(16) NOT NULL DEFAULT 'IDLE',
 bootstrap_cursor CHAR(32) NOT NULL DEFAULT '',
 copied BIGINT NOT NULL DEFAULT 0,
 verified BIGINT NOT NULL DEFAULT 0,
 started_version BIGINT NOT NULL DEFAULT 0,
 cutover_version BIGINT NOT NULL DEFAULT 0,
 updated_at DATETIME(6) NOT NULL
);
INSERT IGNORE INTO search_rebuild_state(id,updated_at) VALUES('jobs',UTC_TIMESTAMP(6));
CREATE TABLE IF NOT EXISTS search_rebuild_deltas (
 job_id CHAR(32) PRIMARY KEY,
 version BIGINT NOT NULL,
 INDEX search_rebuild_delta_version(version)
);

INSERT IGNORE INTO schema_migrations(version) VALUES('search-online-rebuild-v1');
