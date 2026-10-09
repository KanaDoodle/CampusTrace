CREATE TABLE IF NOT EXISTS knowledge_state (
 user_id VARCHAR(32) PRIMARY KEY, revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
 lease_key VARCHAR(32) NOT NULL DEFAULT '', lease_until DATETIME(6) NULL,
 FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS knowledge_vectors (
 user_id VARCHAR(32) NOT NULL, chunk_id VARCHAR(32) NOT NULL,
 model_key CHAR(64) NOT NULL, input_hash CHAR(64) NOT NULL,
 body JSON NOT NULL, created_at DATETIME(6) NOT NULL,
 PRIMARY KEY(user_id,chunk_id,model_key), INDEX(user_id,model_key),
 FOREIGN KEY(user_id) REFERENCES users(id),
 FOREIGN KEY(chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);
