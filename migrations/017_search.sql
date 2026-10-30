CREATE TABLE IF NOT EXISTS search_revision_state (
 id VARCHAR(16) PRIMARY KEY,
 dataset CHAR(32) NOT NULL,
 version BIGINT NOT NULL DEFAULT 0,
 reconcile_cursor CHAR(32) NOT NULL DEFAULT '',
 initial_done BOOLEAN NOT NULL DEFAULT FALSE,
 next_reconcile_at DATETIME(6) NULL
);
INSERT IGNORE INTO search_revision_state(id,dataset) VALUES('jobs',REPLACE(UUID(),'-',''));
CREATE TABLE IF NOT EXISTS search_documents (
 job_id CHAR(32) PRIMARY KEY,
 version BIGINT NOT NULL,
 content_hash CHAR(64) NOT NULL,
 body JSON NOT NULL
);
CREATE TABLE IF NOT EXISTS search_outbox (
 job_id CHAR(32) PRIMARY KEY,
 version BIGINT NOT NULL,
 traceparent VARCHAR(55) NOT NULL DEFAULT '',
 created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 INDEX search_pending_version(version)
);
CREATE TABLE IF NOT EXISTS search_sync_state (
 id VARCHAR(16) PRIMARY KEY,
 generation VARCHAR(160) NOT NULL DEFAULT '',
 ready BOOLEAN NOT NULL DEFAULT FALSE,
 bootstrap_cursor CHAR(32) NOT NULL DEFAULT ''
);
INSERT IGNORE INTO search_sync_state(id) VALUES('jobs');
