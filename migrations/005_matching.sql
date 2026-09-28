CREATE TABLE IF NOT EXISTS job_requirement_cache (
 user_id VARCHAR(32) NOT NULL, content_key CHAR(64) NOT NULL, body JSON NOT NULL,
 PRIMARY KEY(user_id,content_key), FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS job_match_results (
 user_id VARCHAR(32) NOT NULL, job_id VARCHAR(32) NOT NULL, body JSON NOT NULL,
 PRIMARY KEY(user_id,job_id), FOREIGN KEY(user_id) REFERENCES users(id), FOREIGN KEY(job_id) REFERENCES jobs(id)
);
CREATE TABLE IF NOT EXISTS match_settings (
 user_id VARCHAR(32) PRIMARY KEY, body JSON NOT NULL, FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS match_daily_usage (
 user_id VARCHAR(32) NOT NULL, usage_day DATE NOT NULL, calls INT NOT NULL DEFAULT 0,
 PRIMARY KEY(user_id,usage_day), FOREIGN KEY(user_id) REFERENCES users(id)
);
