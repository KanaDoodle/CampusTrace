CREATE TABLE IF NOT EXISTS application_contexts (
 application_id VARCHAR(32) NOT NULL, phase VARCHAR(16) NOT NULL, user_id VARCHAR(32) NOT NULL,
 context_key CHAR(64) NOT NULL, body JSON NOT NULL,
 PRIMARY KEY(application_id,phase), INDEX(user_id,application_id),
 FOREIGN KEY(application_id) REFERENCES applications(id) ON DELETE CASCADE,
 FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS recruitment_notices (
 id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, dedup_key CHAR(64) NOT NULL,
 state VARCHAR(16) NOT NULL, version INT NOT NULL, created_at DATETIME(6) NOT NULL,
 application_id VARCHAR(32) NULL, body JSON NOT NULL,
 UNIQUE(user_id,dedup_key), INDEX(user_id,created_at,id),
 FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
 FOREIGN KEY(application_id) REFERENCES applications(id) ON DELETE CASCADE
);
