package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DSN, Redis, Etcd, HTTP, JWT, LLMURL, LLMKey, LLMModel                           string
	Workers, AnalysisConcurrency, LLMConcurrency, EmbeddingConcurrency, MaxAttempts int
	TaskTimeout, ClaimIdle                                                          time.Duration
}

func Env(k, d string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return d
}
func Int(k string, d int) int {
	v, e := strconv.Atoi(Env(k, strconv.Itoa(d)))
	if e != nil || v < 1 {
		panic(fmt.Sprintf("%s must be positive", k))
	}
	return v
}
func Load() Config {
	return Config{DSN: Env("MYSQL_DSN", "campus:local-campus-only@tcp(127.0.0.1:13306)/campustrace?parseTime=true&loc=UTC"), Redis: Env("REDIS_ADDR", "127.0.0.1:16379"), Etcd: Env("ETCD_ENDPOINTS", "127.0.0.1:12379"), HTTP: Env("HTTP_ADDR", "127.0.0.1:8080"), JWT: os.Getenv("JWT_SECRET"), LLMURL: os.Getenv("LLM_URL"), LLMKey: os.Getenv("LLM_API_KEY"), LLMModel: os.Getenv("LLM_MODEL"), Workers: Int("WORKER_CONCURRENCY", 4), AnalysisConcurrency: Int("ANALYSIS_CONCURRENCY", 2), LLMConcurrency: Int("LLM_CONCURRENCY", 2), EmbeddingConcurrency: Int("EMBEDDING_CONCURRENCY", 2), MaxAttempts: Int("MAX_ATTEMPTS", 4), TaskTimeout: time.Duration(Int("TASK_TIMEOUT_SECONDS", 15)) * time.Second, ClaimIdle: time.Duration(Int("CLAIM_IDLE_SECONDS", 45)) * time.Second}
}
func (c Config) Endpoints() []string { return strings.Split(c.Etcd, ",") }
