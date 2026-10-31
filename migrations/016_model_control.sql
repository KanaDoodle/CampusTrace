CREATE TABLE IF NOT EXISTS model_resource_pools (
 id VARCHAR(64) PRIMARY KEY, capacity INT NOT NULL, queue_limit INT NOT NULL, owner_queue_limit INT NOT NULL,
 daily_limit INT NOT NULL, lease_ms INT NOT NULL, last_owner VARCHAR(32) NOT NULL DEFAULT '', fence BIGINT UNSIGNED NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS model_resource_tickets (
 id CHAR(32) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL, user_id VARCHAR(32) NOT NULL, state VARCHAR(24) NOT NULL,
 token CHAR(32) NOT NULL DEFAULT '', fence BIGINT UNSIGNED NOT NULL DEFAULT 0,
 created_at DATETIME(6) NOT NULL, wait_until DATETIME(6) NOT NULL, lease_until DATETIME(6) NOT NULL, hold_until DATETIME(6) NOT NULL,
 INDEX(pool_id,state,user_id,created_at), INDEX(pool_id,state,lease_until), INDEX(user_id,created_at),
 FOREIGN KEY(pool_id) REFERENCES model_resource_pools(id), FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS model_pool_daily (
 pool_id VARCHAR(64) NOT NULL, usage_day CHAR(10) NOT NULL, allocated INT NOT NULL DEFAULT 0,
 PRIMARY KEY(pool_id,usage_day), FOREIGN KEY(pool_id) REFERENCES model_resource_pools(id)
);
CREATE TABLE IF NOT EXISTS model_call_ledger (
 id CHAR(32) PRIMARY KEY, pool_id VARCHAR(64) NOT NULL, user_id VARCHAR(32) NOT NULL,
 fingerprint CHAR(64) NOT NULL, state VARCHAR(32) NOT NULL, version INT NOT NULL,
 created_at DATETIME(6) NOT NULL, review_hash CHAR(64) NOT NULL DEFAULT '', body JSON NOT NULL,
 INDEX(user_id,state,fingerprint), INDEX(user_id,created_at),
 FOREIGN KEY(id) REFERENCES model_resource_tickets(id), FOREIGN KEY(user_id) REFERENCES users(id)
);
