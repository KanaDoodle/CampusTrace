CREATE TABLE IF NOT EXISTS job_assessment_schedule (
 job_id VARCHAR(32) PRIMARY KEY,
 input_key CHAR(64) NOT NULL DEFAULT '',
 rule_version VARCHAR(64) NOT NULL DEFAULT '',
 next_assess_at DATETIME(6) NULL,
 pending_task_id VARCHAR(32) NULL,
 pending_until DATETIME(6) NULL,
 INDEX assessment_due(next_assess_at,job_id),
 FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
INSERT IGNORE INTO schema_migrations(version) VALUES('assessment-schedule-v1');
