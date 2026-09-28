CREATE TABLE IF NOT EXISTS source_http_cache (
 source_id VARCHAR(32) NOT NULL, request_key CHAR(64) NOT NULL,
 etag VARCHAR(512) NOT NULL, last_modified VARCHAR(128) NOT NULL,
 response MEDIUMBLOB NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(source_id,request_key), INDEX(updated_at),
 FOREIGN KEY(source_id) REFERENCES sources(id) ON DELETE CASCADE
);
INSERT IGNORE INTO schema_migrations(version) VALUES('local-reliability-v1');
