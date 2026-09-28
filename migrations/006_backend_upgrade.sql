CREATE TABLE IF NOT EXISTS match_runs (
 id CHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, request_key VARCHAR(64) NOT NULL,
 state VARCHAR(24) NOT NULL, token CHAR(32) NOT NULL DEFAULT '', lease_until DATETIME(6) NULL,
 updated_at DATETIME(6) NOT NULL, body JSON NOT NULL,
 UNIQUE(user_id,request_key), INDEX(user_id,updated_at,id), INDEX(state,lease_until),
 FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS match_run_events (
 id CHAR(32) PRIMARY KEY, run_id CHAR(32) NOT NULL, occurred_at DATETIME(6) NOT NULL,
 body JSON NOT NULL, INDEX(run_id,occurred_at,id), FOREIGN KEY(run_id) REFERENCES match_runs(id)
);
CREATE TABLE IF NOT EXISTS application_campaigns (
 id CHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, company_id VARCHAR(32) NOT NULL,
 version INT NOT NULL, body JSON NOT NULL, INDEX(user_id,id),
 FOREIGN KEY(user_id) REFERENCES users(id), FOREIGN KEY(company_id) REFERENCES companies(id)
);
CREATE TABLE IF NOT EXISTS application_campaign_jobs (
 user_id VARCHAR(32) NOT NULL, job_id VARCHAR(32) NOT NULL, campaign_id CHAR(32) NOT NULL,
 PRIMARY KEY(user_id,job_id), INDEX(campaign_id,job_id),
 FOREIGN KEY(user_id) REFERENCES users(id), FOREIGN KEY(job_id) REFERENCES jobs(id),
 FOREIGN KEY(campaign_id) REFERENCES application_campaigns(id)
);
