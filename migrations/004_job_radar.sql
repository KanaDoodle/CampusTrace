CREATE TABLE IF NOT EXISTS watch_targets (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, source_id VARCHAR(32) NOT NULL,
 enabled BOOLEAN NOT NULL, next_check_at DATETIME(6) NOT NULL, body JSON NOT NULL,
 INDEX due_watch(enabled,next_check_at,id), INDEX owner_watch(user_id,id),
 FOREIGN KEY(user_id) REFERENCES users(id), FOREIGN KEY(source_id) REFERENCES sources(id)
);
CREATE TABLE IF NOT EXISTS watch_results (
 watch_id VARCHAR(32) NOT NULL, schedule_version BIGINT UNSIGNED NOT NULL,
 posting_key CHAR(64) NOT NULL, outcome VARCHAR(16) NOT NULL, observation_id VARCHAR(32) NOT NULL,
 PRIMARY KEY(watch_id,schedule_version,posting_key,outcome),
 FOREIGN KEY(watch_id) REFERENCES watch_targets(id) ON DELETE CASCADE,
 FOREIGN KEY(observation_id) REFERENCES observations(id)
);
CREATE TABLE IF NOT EXISTS watch_postings (
 watch_id VARCHAR(32) NOT NULL, posting_key CHAR(64) NOT NULL, body JSON NOT NULL,
 PRIMARY KEY(watch_id,posting_key), FOREIGN KEY(watch_id) REFERENCES watch_targets(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS user_job_preferences (
 user_id VARCHAR(32) NOT NULL, job_id VARCHAR(32) NOT NULL, disposition VARCHAR(16) NOT NULL,
 PRIMARY KEY(user_id,job_id), FOREIGN KEY(user_id) REFERENCES users(id), FOREIGN KEY(job_id) REFERENCES jobs(id),
 CHECK(disposition IN ('NONE','SAVED','IGNORED'))
);
CREATE TABLE IF NOT EXISTS notifications (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, dedup_key CHAR(64) NOT NULL,
 created_at DATETIME(6) NOT NULL, read_at DATETIME(6) NULL, body JSON NOT NULL,
 UNIQUE notification_fact(user_id,dedup_key), INDEX inbox(user_id,created_at,id),
 FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS watch_runs (
 watch_id VARCHAR(32) NOT NULL, schedule_version BIGINT UNSIGNED NOT NULL,
 expected_count INT NOT NULL, completed_count INT NOT NULL DEFAULT 0, failed_count INT NOT NULL DEFAULT 0,
 PRIMARY KEY(watch_id,schedule_version), FOREIGN KEY(watch_id) REFERENCES watch_targets(id) ON DELETE CASCADE
);
INSERT IGNORE INTO schema_migrations(version) VALUES('job-radar-v4');
