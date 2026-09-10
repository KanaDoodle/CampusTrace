package integration

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/redis/go-redis/v9"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestRepairPoisonWorkerSubprocess(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("requires integration dependencies")
	}
	if os.Getenv("CT_POISON_REPAIR_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRepairPoisonWorkerSubprocess$", "-test.count=1")
		cmd.Env = append(os.Environ(), "CT_POISON_REPAIR_CHILD=1")
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("F04: worker subprocess failed: %v\n%s", e, out)
		}
		return
	}
	ctx, s, q, _, _ := setup(t)
	must(t, q.Init(ctx))
	raw := []string{"{bad"}
	for _, mode := range []string{"zero", "negative", "unknown", "missing-id"} {
		task := p.NewTask("ANALYZE", "missing")
		switch mode {
		case "zero":
			task.Attempt = 0
		case "negative":
			task.Attempt = -1
		case "unknown":
			task.Type = "OTHER"
		case "missing-id":
			task.ID = ""
		}
		raw = append(raw, d.JSON(task))
	}
	for _, v := range raw {
		must(t, q.R.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), Values: map[string]any{"task": v}}).Err())
	}
	runctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	must(t, worker(s, q).Run(runctx))
	if q.R.HLen(ctx, q.Prefix+"poison").Val() != int64(len(raw)) {
		t.Fatal("worker did not quarantine every poison message")
	}
}
