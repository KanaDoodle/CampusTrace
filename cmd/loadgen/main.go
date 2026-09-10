package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"log"
	"sort"
	"time"
)

func main() {
	n := flag.Int("n", 100, "synthetic observations (1..10000)")
	unique := flag.Bool("unique-content", false, "include a batch nonce to force cold analysis-cache inputs")
	timeout := flag.Duration("timeout", 2*time.Minute, "completion deadline")
	flag.Parse()
	if *n < 1 || *n > 10000 {
		log.Fatal("n must be 1..10000")
	}
	ctx, cancel := bootstrap.Root()
	defer cancel()
	app, err := bootstrap.Open(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()
	ids := map[string]time.Time{}
	start := time.Now()
	batch := d.ID()
	for i := 0; i < *n; i++ {
		nonce := ""
		if *unique {
			nonce = "\nsynthetic batch " + batch
		}
		submittedAt := time.Now()
		o, err := app.Store.Ingest(ctx, p.Ingest{Company: "Synthetic Loadgen", Title: "Go backend load fixture", JobType: "FULL_TIME", Locations: []string{"Shanghai"}, SourceID: "manual", ExternalID: fmt.Sprintf("%s-%d", batch, i), Text: fmt.Sprintf("graduation: 2027\ndegree: BACHELOR\njob_type: FULL_TIME\ntech: go\napply: PRESENT\nsynthetic fixture %d", i) + nonce, FetchStatus: "SUCCESS"})
		if err != nil {
			log.Fatal(err)
		}
		ids[o.ID] = submittedAt
	}
	latencies := []float64{}
	deadline := start.Add(*timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for len(ids) > 0 && time.Now().Before(deadline) && ctx.Err() == nil {
		<-ticker.C
		for id, created := range ids {
			var completed time.Time
			err := app.Store.DB.QueryRowContext(ctx, "SELECT created_at FROM analysis_results WHERE observation_id=? AND analysis_version=?", id, d.EffectiveAnalysisVersion(config.Env("ANALYSIS_VERSION", d.AnalysisVersion))).Scan(&completed)
			if err == nil {
				latencies = append(latencies, completed.Sub(created).Seconds())
				delete(ids, id)
			}
		}
	}
	failed := 0
	if entries, err := app.Queue.DLQ(ctx); err == nil {
		for _, raw := range entries {
			var f pipeline.Failure
			if json.Unmarshal([]byte(raw), &f) == nil {
				if _, ok := ids[f.Task.EntityID]; ok {
					failed++
				}
			}
		}
	}
	sort.Float64s(latencies)
	quantile := func(q float64) float64 {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[int(float64(len(latencies)-1)*q)]
	}
	duration := time.Since(start).Seconds()
	fmt.Println(d.JSON(map[string]any{"mode": "synthetic local analysis pipeline; not production capacity", "unique_content": *unique, "submitted": *n, "processed": len(latencies), "failed": failed, "unfinished": len(ids) - failed, "duration_seconds": duration, "throughput_per_second": float64(len(latencies)) / duration, "p50_seconds": quantile(.5), "p95_seconds": quantile(.95), "p99_seconds": quantile(.99)}))
}
