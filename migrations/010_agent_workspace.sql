CREATE TABLE IF NOT EXISTS agent_memory_state (
 user_id VARCHAR(32) PRIMARY KEY, revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
 FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS agent_memories (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, version BIGINT UNSIGNED NOT NULL,
 deleted BOOLEAN NOT NULL DEFAULT FALSE, expires_at DATETIME(6) NOT NULL,
 updated_at DATETIME(6) NOT NULL, body JSON NOT NULL,
 INDEX(user_id,deleted,expires_at), FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS agent_tasks (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, created_at DATETIME(6) NOT NULL,
 expires_at DATETIME(6) NOT NULL, body JSON NOT NULL,
 INDEX(user_id,created_at), FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS practice_runs (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, request_key VARCHAR(64) NOT NULL,
 fingerprint CHAR(64) NOT NULL, state VARCHAR(32) NOT NULL, created_at DATETIME(6) NOT NULL,
 body JSON NOT NULL, UNIQUE(user_id,request_key), INDEX(user_id,created_at),
 FOREIGN KEY(user_id) REFERENCES users(id)
);
