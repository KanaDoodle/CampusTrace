CREATE TABLE IF NOT EXISTS analysis_generations (
 observation_id VARCHAR(32) NOT NULL,
 processing_version VARCHAR(64) NOT NULL,
 generation BIGINT UNSIGNED NOT NULL,
 PRIMARY KEY(observation_id, processing_version),
 UNIQUE KEY observation_generation(observation_id, generation)
);
CREATE TABLE IF NOT EXISTS analysis_task_generations (
 task_id VARCHAR(32) PRIMARY KEY,
 observation_id VARCHAR(32) NOT NULL,
 processing_version VARCHAR(64) NOT NULL,
 generation BIGINT UNSIGNED NOT NULL
);
CREATE TABLE IF NOT EXISTS evaluation_cache (
 user_id VARCHAR(32) NOT NULL,
 job_id VARCHAR(32) NOT NULL,
 body JSON NOT NULL,
 PRIMARY KEY(user_id,job_id)
);
