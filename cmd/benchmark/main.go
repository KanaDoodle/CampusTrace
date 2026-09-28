// benchmark operates only on an explicitly named disposable schema. It never
// sends model requests or reads the application's configured production DSN.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/go-sql-driver/mysql"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"
	"time"
)

type sample struct {
	Name        string  `json:"name"`
	Requests    int     `json:"requests"`
	Concurrency int     `json:"concurrency"`
	Errors      int     `json:"errors"`
	P50         float64 `json:"p50_ms"`
	P95         float64 `json:"p95_ms"`
	P99         float64 `json:"p99_ms"`
	RPS         float64 `json:"requests_per_second"`
	Allocated   uint64  `json:"allocated_bytes_per_request"`
	Allocs      uint64  `json:"allocations_per_request"`
}

func measure(ctx context.Context, name string, n, c int, fn func(context.Context) error) sample {
	// Warm pools and the process-local parsed-JD cache before collecting samples.
	for i := 0; i < 3; i++ {
		_ = fn(ctx)
	}
	out := sample{Name: name, Requests: n, Concurrency: c}
	durations := make([]float64, n)
	jobs := make(chan int)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i := 0; i < c; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				at := time.Now()
				err := fn(ctx)
				durations[j] = float64(time.Since(at).Microseconds()) / 1000
				if err != nil {
					mu.Lock()
					out.Errors++
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	runtime.ReadMemStats(&after)
	out.Allocated = (after.TotalAlloc - before.TotalAlloc) / uint64(n)
	out.Allocs = (after.Mallocs - before.Mallocs) / uint64(n)
	sort.Float64s(durations)
	percentile := func(pct float64) float64 { return durations[int(float64(n-1)*pct)] }
	out.P50 = percentile(.50)
	out.P95 = percentile(.95)
	out.P99 = percentile(.99)
	out.RPS = float64(n) / time.Since(start).Seconds()
	return out
}
func run() error {
	count := flag.Int("jobs", 1000, "synthetic jobs (100..10000)")
	requests := flag.Int("requests", 50, "requests per scenario")
	concurrency := flag.Int("concurrency", 4, "read concurrency")
	uniqueText := flag.Bool("unique-text", false, "use distinct descriptions rather than a shared synthetic JD")
	scenario := flag.String("scenario", "", "measure only the named scenario")
	cpuPath := flag.String("cpu-profile", "", "write a CPU profile of measured scenarios to a new file")
	heapPath := flag.String("heap-profile", "", "write a post-GC heap profile to a new file")
	flag.Parse()
	if *count < 100 || *count > 10000 || *requests < 10 || *requests > 500 || *concurrency < 1 || *concurrency > 16 {
		return errors.New("benchmark bounds invalid")
	}
	cfg, err := mysql.ParseDSN(os.Getenv("MYSQL_BENCH_DSN"))
	if err != nil || !regexp.MustCompile(`^campustrace_bench_[A-Za-z0-9_]+$`).MatchString(cfg.DBName) {
		return errors.New("MYSQL_BENCH_DSN must point to a disposable campustrace_bench_* schema")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s, err := p.Open(ctx, os.Getenv("MYSQL_BENCH_DSN"))
	if err != nil {
		return err
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		return err
	}
	u, err := s.NewUser(ctx, d.ID()+"@benchmark.invalid", "unused")
	if err != nil {
		return err
	}
	if err = s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, Skills: []string{"MySQL", "Redis"}, TargetRoles: []string{"后端开发"}}); err != nil {
		return err
	}
	source := d.ID()
	if err = s.SaveSource(ctx, d.Source{ID: source, Name: "Synthetic benchmark", Type: "OFFICIAL", Trust: "OFFICIAL", Visibility: "GLOBAL"}); err != nil {
		return err
	}
	ids := []string{}
	text := "岗位职责：负责服务端开发、任务调度和数据库优化。\n岗位要求：熟悉 Go 或 Java 任意一种语言；了解 MySQL、Redis；掌握计算机网络。\n加分项：具备 Docker 使用经验。"
	at := time.Now().UTC()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	companyIDs := []string{}
	// 100 companies keep comparison scopes below the existing 200-job limit.
	for i := 0; i < 100; i++ {
		id := d.ID()
		companyIDs = append(companyIDs, id)
		name := fmt.Sprintf("Synthetic Company %03d", i)
		if _, err = tx.ExecContext(ctx, "INSERT INTO companies(id,normalized_name,body) VALUES(?,?,?)", id, d.Normalize(name), d.JSON(d.Company{ID: id, Name: name})); err != nil {
			return err
		}
	}
	for i := 0; i < *count; i++ {
		id := d.ID()
		j := d.Job{ID: id, CompanyID: companyIDs[i%100], Company: fmt.Sprintf("Synthetic Company %03d", i%100), Title: fmt.Sprintf("后端开发 %04d", i), JobType: "FULL_TIME", Locations: []string{"上海市"}, CurrentStatus: "UNKNOWN", UpdatedAt: at}
		if _, err = tx.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,visibility,owner_id,body) VALUES(?,?,?,'GLOBAL','',?)", id, j.CompanyID, d.Hash(id), d.JSON(j)); err != nil {
			return err
		}
		posting := d.ID()
		if _, err = tx.ExecContext(ctx, "INSERT INTO postings(id,job_id,source_id,source_key,body) VALUES(?,?,?,?,?)", posting, id, source, id, d.JSON(d.Posting{ID: posting, JobID: id, SourceID: source})); err != nil {
			return err
		}
		jobText := text
		if *uniqueText {
			jobText += fmt.Sprintf("\n内部岗位标记：%04d", i)
		}
		obs := d.Observation{ID: d.ID(), JobID: id, PostingID: posting, Text: jobText, Hash: d.Hash(jobText), ObservedAt: at, FetchStatus: "SUCCESS", Trust: "OFFICIAL"}
		if _, err = tx.ExecContext(ctx, "INSERT INTO observations(id,job_id,posting_id,observed_at,body) VALUES(?,?,?,?,?)", obs.ID, id, posting, at, d.JSON(obs)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO job_match_results(user_id,job_id,body) VALUES(?,?,?)", u, id, d.JSON(matching.Result{JobID: id, InputKey: "synthetic-obsolete", Model: "benchmark"})); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	sqlCase := func(indexed bool) func(context.Context) error {
		projection := "JSON_UNQUOTE(JSON_EXTRACT(body,'$.company'))"
		if indexed {
			projection = "company_name"
		}
		hint := ""
		if !indexed {
			hint = " IGNORE INDEX(jobs_company_visible)"
		}
		query := "SELECT body FROM jobs" + hint + " WHERE " + projection + "=? AND (visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?)) ORDER BY id LIMIT 201"
		return func(ctx context.Context) error {
			_, err := p.Many[d.Job](ctx, s.DB, query, "Synthetic Company 000", u)
			return err
		}
	}
	resultsCase := func(selected bool) func(context.Context) error {
		query := "SELECT JSON_OBJECT('job_id',job_id,'input_key',JSON_UNQUOTE(JSON_EXTRACT(body,'$.input_key')),'model',JSON_UNQUOTE(JSON_EXTRACT(body,'$.model')),'score',JSON_EXTRACT(body,'$.score'),'coverage',JSON_EXTRACT(body,'$.coverage')) FROM job_match_results WHERE user_id=?"
		args := []any{u}
		if selected {
			query += " AND job_id IN (?,?,?)"
			for _, id := range ids[:3] {
				args = append(args, id)
			}
		}
		return func(ctx context.Context) error {
			_, err := p.Many[matching.Result](ctx, s.DB, query, args...)
			return err
		}
	}
	cases := []struct {
		name        string
		concurrency int
		fn          func(context.Context) error
	}{
		{"selected_results_before_all_user_results", 1, resultsCase(false)},
		{"selected_results_after_3_results", 1, resultsCase(true)},
		{"company_query_json_expression", 1, sqlCase(false)},
		{"company_query_index", 1, sqlCase(true)},
		{"selected_3_jobs", *concurrency, func(ctx context.Context) error {
			_, err := s.MatchSnapshot(ctx, u, "benchmark", "", ids[:3])
			return err
		}}, {"company_comparison", *concurrency, func(ctx context.Context) error {
			_, err := s.MatchDecisionSnapshot(ctx, u, "benchmark", "", nil, "Synthetic Company 000")
			return err
		}}, {"all_jobs_local_screen", *concurrency, func(ctx context.Context) error { _, err := s.MatchSnapshot(ctx, u, "benchmark", "", nil); return err }},
	}
	if *scenario != "" {
		selected := cases[:0]
		for _, c := range cases {
			if c.name == *scenario {
				selected = append(selected, c)
			}
		}
		if len(selected) == 0 {
			return errors.New("unknown benchmark scenario")
		}
		cases = selected
	}
	var cpu, heap *os.File
	if *cpuPath != "" {
		cpu, err = os.OpenFile(*cpuPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer cpu.Close()
		if err = pprof.StartCPUProfile(cpu); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	if *heapPath != "" {
		heap, err = os.OpenFile(*heapPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer heap.Close()
	}
	samples := []sample{}
	for _, c := range cases {
		samples = append(samples, measure(ctx, c.name, *requests, c.concurrency, c.fn))
	}
	if cpu != nil {
		pprof.StopCPUProfile()
		if err = cpu.Close(); err != nil {
			return err
		}
	}
	if heap != nil {
		runtime.GC()
		if err = pprof.WriteHeapProfile(heap); err != nil {
			return err
		}
		if err = heap.Close(); err != nil {
			return err
		}
	}
	stats := s.DB.Stats()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"jobs": *count, "unique_text": *uniqueText, "cpu_profile": cpu != nil, "heap_profile": heap != nil, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(), "samples": samples, "db_max_connections": stats.MaxOpenConnections, "db_wait_count": stats.WaitCount, "db_wait_ms": float64(stats.WaitDuration.Microseconds()) / 1000, "heap_bytes": mem.HeapAlloc, "model_calls": 0})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "benchmark failed:", err)
		os.Exit(1)
	}
}
