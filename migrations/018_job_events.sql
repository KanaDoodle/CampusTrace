CREATE TABLE IF NOT EXISTS job_event_outbox (
 event_id CHAR(64) PRIMARY KEY,
 dataset CHAR(32) NOT NULL,
 job_id CHAR(32) NOT NULL,
 event_version BIGINT NOT NULL,
 occurred_at DATETIME(6) NOT NULL,
 published_at DATETIME(6) NULL,
 body JSON NOT NULL,
 UNIQUE KEY job_event_version(dataset,event_version),
 INDEX job_event_pending(published_at,event_version)
);
CREATE TABLE IF NOT EXISTS job_notification_receipts (
 event_id CHAR(64) PRIMARY KEY,
 dataset CHAR(32) NOT NULL,
 job_id CHAR(32) NOT NULL,
 processed_at DATETIME(6) NOT NULL
);
CREATE TABLE IF NOT EXISTS job_notification_versions (
 dataset CHAR(32) NOT NULL,
 job_id CHAR(32) NOT NULL,
 version BIGINT NOT NULL,
 PRIMARY KEY(dataset,job_id)
);
