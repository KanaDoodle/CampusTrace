CREATE TABLE IF NOT EXISTS outbox_tenants (
 task_id VARCHAR(32) PRIMARY KEY, owner_id VARCHAR(32) NOT NULL, created_at DATETIME(6) NOT NULL,
 INDEX(owner_id,created_at,task_id), FOREIGN KEY(task_id) REFERENCES outbox(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS queue_dispatch_clock (id VARCHAR(32) PRIMARY KEY,last_owner VARCHAR(32) NOT NULL);
INSERT IGNORE INTO queue_dispatch_clock(id,last_owner) VALUES('outbox','');
CREATE TABLE IF NOT EXISTS queue_admission_lock (id VARCHAR(32) PRIMARY KEY);
INSERT IGNORE INTO queue_admission_lock(id) VALUES('outbox');
CREATE TABLE IF NOT EXISTS analysis_invocations (
 observation_id VARCHAR(32) NOT NULL, version_hash CHAR(64) NOT NULL,
 task_id VARCHAR(32) NOT NULL, started_at DATETIME(6) NOT NULL,
 PRIMARY KEY(observation_id,version_hash),
 FOREIGN KEY(observation_id) REFERENCES observations(id) ON DELETE CASCADE
);
