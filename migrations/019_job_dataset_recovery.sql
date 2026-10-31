CREATE TABLE IF NOT EXISTS job_dataset_recovery (
 id VARCHAR(16) PRIMARY KEY,
 restore_schema VARCHAR(64) NOT NULL,
 previous_dataset CHAR(32) NOT NULL,
 current_dataset CHAR(32) NOT NULL,
 prepared_at DATETIME(6) NOT NULL
);
