CREATE TABLE IF NOT EXISTS task_queue_namespaces (
 namespace_id CHAR(64) PRIMARY KEY,
 epoch CHAR(32) NOT NULL,
 legacy_imported BOOLEAN NOT NULL DEFAULT FALSE,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);
CREATE TABLE IF NOT EXISTS task_queue_lanes (
 namespace_id CHAR(64) NOT NULL,
 lane VARCHAR(32) NOT NULL,
 last_owner VARCHAR(32) NOT NULL DEFAULT '',
 capacity INT NOT NULL,
 owner_capacity INT NOT NULL,
 PRIMARY KEY(namespace_id,lane)
);
CREATE TABLE IF NOT EXISTS kafka_tasks (
 namespace_id CHAR(64) NOT NULL,
 task_id VARCHAR(100) NOT NULL,
 lane VARCHAR(32) NOT NULL,
 owner_id VARCHAR(32) NOT NULL,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 state VARCHAR(16) NOT NULL,
 body MEDIUMBLOB NOT NULL,
 body_hash CHAR(64) NOT NULL,
 lease_token CHAR(32) NOT NULL DEFAULT '',
 lease_until DATETIME(6) NULL,
 due_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 category VARCHAR(32) NOT NULL DEFAULT '',
 dlq_published BOOLEAN NOT NULL DEFAULT FALSE,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY(namespace_id,task_id),
 INDEX task_ready(namespace_id,lane,state,due_at,owner_id),
 INDEX task_owner(namespace_id,lane,owner_id,state),
 INDEX task_cleanup(namespace_id,state,updated_at)
);
CREATE TABLE IF NOT EXISTS kafka_task_poison (
 namespace_id CHAR(64) NOT NULL,
 record_key CHAR(64) NOT NULL,
 body JSON NOT NULL,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY(namespace_id,record_key)
);

INSERT IGNORE INTO schema_migrations(version) VALUES('kafka-tasks-v1');
