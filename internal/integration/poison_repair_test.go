package integration

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/twmb/franz-go/pkg/kgo"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRepairPoisonWorkerSubprocess(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("requires integration dependencies")
	}
	if os.Getenv("CT_POISON_REPAIR_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRepairPoisonWorkerSubprocess$", "-test.count=1")
		cmd.Env = append(os.Environ(), "CT_POISON_REPAIR_CHILD=1")
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("F04: worker subprocess failed: %v\n%s", e, out)
		}
		return
	}
	t.Setenv("KAFKA_TASK_PREFIX", "test.poison."+d.ID())
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
	client, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(os.Getenv("KAFKA_BROKERS"), ",")...))
	must(t, err)
	defer client.Close()
	for _, v := range raw {
		must(t, client.ProduceSync(ctx, &kgo.Record{Topic: q.Broker.Topic("SYSTEM_ANALYZE"), Value: []byte(v)}).FirstErr())
	}
	runctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	must(t, worker(s, q).Run(runctx))
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM kafka_task_poison").Scan(&count))
	if count != len(raw) {
		t.Fatal("worker did not quarantine every poison message")
	}
}
