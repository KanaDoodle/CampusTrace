CREATE TABLE IF NOT EXISTS company_match_reports (
 user_id VARCHAR(32) NOT NULL, company_name VARCHAR(255) NOT NULL, input_key CHAR(64) NOT NULL,
 body JSON NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(user_id,company_name,input_key), INDEX(user_id,updated_at),
 FOREIGN KEY(user_id) REFERENCES users(id)
);
