package integration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	rpc "github.com/KanaDoodle/KanaRPC"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type cancelAwareExtractor struct {
	calls    atomic.Int32
	canceled chan struct{}
}

func (e *cancelAwareExtractor) Claims(ctx context.Context, _ string) ([]d.Claim, error) {
	e.calls.Add(1)
	<-ctx.Done()
	close(e.canceled)
	return nil, ctx.Err()
}

func TestKanaRPCInvocationFenceAndDiscovery(t *testing.T) {
	t.Run("not_dispatched_can_retry_after_registration", func(t *testing.T) {
		ctx, s, q, _, src := setup(t)
		name := "ct-admission-" + d.ID()
		client, err := analysis.NewClient(config.Load().Endpoints(), 1, time.Second, name)
		must(t, err)
		defer client.Close()
		o, err := s.Ingest(ctx, ingest(t, ctx, s, src))
		must(t, err)
		w := worker(s, q)
		w.Analyzer = client
		task := p.NewTask("ANALYZE", o.ID)
		err = w.Process(ctx, task)
		if !analysis.InvocationRejected(err) || !pipeline.Transient(err) {
			t.Fatal("not-dispatched refusal", err)
		}
		var count int
		must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM analysis_invocations WHERE observation_id=?", o.ID).Scan(&count))
		if count != 0 {
			t.Fatal("refusal left a paid-call fence", count)
		}
		srv, err := analysis.StartServer(ctx, config.Load().Endpoints(), "127.0.0.1:0", "", "admission", analysis.RuleExtractor{}, 1, 0, name)
		must(t, err)
		defer srv.Close()
		deadline := time.Now().Add(2 * time.Second)
		for client.Stats().Endpoints != 1 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		must(t, w.Process(ctx, task))
		claims, err := s.Evidence(ctx, o.JobID)
		must(t, err)
		if len(claims) == 0 {
			t.Fatal("retry lost analysis result")
		}
	})
	t.Run("dispatched_timeout_cancels_and_stays_uncertain", func(t *testing.T) {
		ctx, s, q, _, src := setup(t)
		name := "ct-uncertain-" + d.ID()
		extractor := &cancelAwareExtractor{canceled: make(chan struct{})}
		srv, err := analysis.StartServer(ctx, config.Load().Endpoints(), "127.0.0.1:0", "", "uncertain", extractor, 1, 0, name)
		must(t, err)
		defer srv.Close()
		client, err := analysis.NewClient(config.Load().Endpoints(), 1, 150*time.Millisecond, name)
		must(t, err)
		defer client.Close()
		o, err := s.Ingest(ctx, ingest(t, ctx, s, src))
		must(t, err)
		w := worker(s, q)
		w.Analyzer = client
		task := p.NewTask("ANALYZE", o.ID)
		for i := 0; i < 2; i++ {
			if err := w.Process(ctx, task); !errors.Is(err, pipeline.ErrAnalysisUncertain) {
				t.Fatal(err)
			}
		}
		select {
		case <-extractor.canceled:
		case <-time.After(time.Second):
			t.Fatal("server work survived caller deadline")
		}
		if extractor.calls.Load() != 1 {
			t.Fatal("uncertain paid invocation replayed", extractor.calls.Load())
		}
		var count int
		must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM analysis_invocations WHERE observation_id=?", o.ID).Scan(&count))
		if count != 1 {
			t.Fatal("unknown invocation fence lost", count)
		}
	})
}

type drainAwareExtractor struct{ started, release chan struct{} }

func (e *drainAwareExtractor) Claims(ctx context.Context, text string) ([]d.Claim, error) {
	close(e.started)
	select {
	case <-e.release:
		return analysis.Extract(ctx, text)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestKanaRPCRegisteredShutdownDrainsAfterParentSignal(t *testing.T) {
	ctx, _, _, _, _ := setup(t)
	parent, cancel := context.WithCancel(ctx)
	defer cancel()
	extractor := &drainAwareExtractor{started: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-extractor.release:
		default:
			close(extractor.release)
		}
	}()
	name := "ct-drain-" + d.ID()
	srv, err := analysis.StartServer(parent, config.Load().Endpoints(), "127.0.0.1:0", "", "drain", extractor, 1, 0, name)
	must(t, err)
	defer srv.Close()
	client, err := analysis.NewClient(config.Load().Endpoints(), 1, 3*time.Second, name)
	must(t, err)
	defer client.Close()
	called := make(chan error, 1)
	go func() {
		out, err := client.Call(ctx, analysis.Request{Text: jd})
		if err == nil && len(out.Claims) == 0 {
			err = errors.New("in-flight response lost")
		}
		called <- err
	}()
	select {
	case <-extractor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("extractor not started")
	}
	cancel() // main initiates Close on SIGTERM; the handler must remain drainable.
	type outcome struct {
		report rpc.DrainReport
		err    error
	}
	drained := make(chan outcome, 1)
	go func() {
		budget, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		report, err := srv.Shutdown(budget)
		drained <- outcome{report, err}
	}()
	deadline := time.Now().Add(time.Second)
	for srv.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if srv.Ready() {
		t.Fatal("still ready while draining")
	}
	cli, err := clientv3.New(clientv3.Config{Endpoints: config.Load().Endpoints(), DialTimeout: time.Second})
	must(t, err)
	defer cli.Close()
	for {
		registration, err := cli.Get(ctx, srv.RegistrationKey())
		must(t, err)
		if len(registration.Kvs) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("registration not withdrawn before drain")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case result := <-drained:
		t.Fatal("shutdown aborted in-flight analysis", result.err)
	default:
	}
	close(extractor.release)
	must(t, <-called)
	result := <-drained
	must(t, result.err)
	if !result.report.Quiescent || result.report.AddedFaults != (rpc.DeliveryFaults{}) {
		t.Fatal(result.report)
	}
	if stats := srv.Registry.Stats(); !stats.Closed || stats.Registrations != 0 {
		t.Fatal("registration lifecycle retained", stats)
	}
	client.Close()
	if stats := client.Stats(); !stats.Closed || stats.Connections != 0 || stats.ActiveCalls != 0 {
		t.Fatal("client resources retained", stats)
	}
}

func TestKanaRPCDiscoveryRemainsFreshAfterIdle(t *testing.T) {
	ctx, _, _, _, _ := setup(t)
	name := "ct-idle-" + d.ID()
	client, err := analysis.NewClient(config.Load().Endpoints(), 1, time.Second, name)
	must(t, err)
	defer client.Close()
	srv, err := analysis.StartServer(ctx, config.Load().Endpoints(), "127.0.0.1:0", "", "idle", analysis.RuleExtractor{}, 1, 0, name)
	must(t, err)
	defer srv.Close()
	// Establish one discovery view, then cross two freshness windows without
	// business traffic. Each sample must stay fresh, not only the final RPC.
	deadline := time.Now().Add(2 * time.Second)
	for (!client.Stats().Ready || client.Stats().Endpoints != 1) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	until := time.Now().Add(7 * time.Second)
	for time.Now().Before(until) {
		stats := client.Stats()
		if !stats.Ready {
			recovery := time.Now().Add(2 * time.Second)
			for !client.Stats().Ready && time.Now().Before(recovery) {
				time.Sleep(time.Millisecond)
			}
			if !client.Stats().Ready {
				t.Fatal("expired discovery did not resnapshot", client.Stats())
			}
		}
		if client.Stats().Endpoints != 1 {
			t.Fatal("idle discovery lost registered instance", client.Stats())
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, err = client.Call(ctx, analysis.Request{Text: jd})
	must(t, err)
}
