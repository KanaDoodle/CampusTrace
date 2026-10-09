CREATE TABLE IF NOT EXISTS agent_executions (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, request_key VARCHAR(64) NOT NULL,
 fingerprint CHAR(64) NOT NULL, state VARCHAR(32) NOT NULL, created_at DATETIME(6) NOT NULL,
 lease_token VARCHAR(32) NOT NULL DEFAULT '', lease_until DATETIME(6) NULL, body JSON NOT NULL,
 UNIQUE(user_id,request_key), INDEX(user_id,created_at), FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS agent_connectors (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, version BIGINT UNSIGNED NOT NULL,
 body JSON NOT NULL, INDEX(user_id), FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS agent_events (
 id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, owner_id VARCHAR(32) NOT NULL DEFAULT '',
 job_id VARCHAR(32) NOT NULL DEFAULT '', kind VARCHAR(32) NOT NULL, created_at DATETIME(6) NOT NULL,
 INDEX(owner_id,id)
);
CREATE TABLE IF NOT EXISTS agent_feed_settings (
 user_id VARCHAR(32) PRIMARY KEY, enabled BOOLEAN NOT NULL DEFAULT FALSE, event_cursor BIGINT UNSIGNED NOT NULL DEFAULT 0,
 FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS agent_todos (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, event_id BIGINT UNSIGNED NOT NULL,
 state VARCHAR(16) NOT NULL DEFAULT 'OPEN', created_at DATETIME(6) NOT NULL, body JSON NOT NULL,
 UNIQUE(user_id,event_id), INDEX(user_id,state,created_at), FOREIGN KEY(user_id) REFERENCES users(id)
);
