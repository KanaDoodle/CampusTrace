package analysis

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	rpc "github.com/KanaDoodle/KanaRPC"
	"github.com/KanaDoodle/KanaRPC/discovery"
	"github.com/KanaDoodle/KanaRPC/routing"
)

type extractorFunc func(context.Context, string) ([]d.Claim, error)

func (f extractorFunc) Claims(ctx context.Context, text string) ([]d.Claim, error) {
	return f(ctx, text)
}

func awaitRPC(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("RPC condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func loopbackRPC(t *testing.T, extractor Extractor, limit int) (*rpc.Server, *rpc.Client, net.Addr) {
	t.Helper()
	s, err := newRPCServer(&Service{Instance: "loopback", Extractor: extractor}, limit)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(listener) }()
	t.Cleanup(func() {
		s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.Wait(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	awaitRPC(t, s.Ready)
	c, err := rpc.Dial(context.Background(), listener.Addr().String(), rpc.ClientConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return s, c, listener.Addr()
}

func TestKanaRPCRemoteCancellationAndReuse(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s, c, _ := loopbackRPC(t, extractorFunc(func(ctx context.Context, text string) ([]d.Claim, error) {
		if calls.Add(1) == 1 {
			if f := observability.From(ctx); f.TaskID != "task-fixture" || f.RequestID != "request-fixture" {
				return nil, errors.New("missing correlation")
			}
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		}
		return Extract(ctx, text)
	}), 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var out Response
		done <- c.Call(ctx, MethodAnalyze, Request{Text: "tech: go", TaskID: "task-fixture", CorrelationID: "request-fixture"}, &out)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("extractor did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation identity", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("CANCEL did not reach extractor before its 2s deadline")
	}
	awaitRPC(t, func() bool { return s.Stats().InFlight == 0 && s.Stats().Executing == 0 })
	var out Response
	if err := c.Call(context.Background(), MethodAnalyze, Request{Text: "tech: go"}, &out); err != nil || len(out.Claims) == 0 {
		t.Fatal("connection reuse", out, err)
	}
	if s.Stats().Connections != 1 || calls.Load() != 2 || s.Stats().Outcomes.CancelRequests != 1 {
		t.Fatal(s.Stats(), calls.Load())
	}
}

func TestKanaRPCDeadlineStopsExtractor(t *testing.T) {
	stopped := make(chan error, 1)
	_, c, _ := loopbackRPC(t, extractorFunc(func(ctx context.Context, _ string) ([]d.Claim, error) {
		<-ctx.Done()
		stopped <- ctx.Err()
		return nil, ctx.Err()
	}), 1)
	var out Response
	err := c.Call(context.Background(), MethodAnalyze, Request{Deadline: time.Now().Add(60 * time.Millisecond)}, &out)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("extractor retained")
	}
}

func TestKanaRPCDrainPreservesInFlightResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s, c, _ := loopbackRPC(t, extractorFunc(func(ctx context.Context, text string) ([]d.Claim, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return Extract(ctx, text)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}), 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		var out Response
		err := c.Call(context.Background(), MethodAnalyze, Request{Text: "tech: go"}, &out)
		if err == nil && len(out.Claims) == 0 {
			err = errors.New("result lost")
		}
		done <- err
	}()
	<-started
	drained := make(chan rpc.DrainReport, 1)
	drainErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		report, err := s.DrainWithReport(ctx)
		drained <- report
		drainErr <- err
	}()
	awaitRPC(t, func() bool { return !s.Ready() })
	var out Response
	err := c.Call(context.Background(), MethodAnalyze, Request{Text: "tech: go"}, &out)
	if err == nil || calls.Load() != 1 {
		t.Fatal("drain accepted new work", err, calls.Load())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal("in-flight result", err)
	}
	report := <-drained
	if err := <-drainErr; err != nil {
		t.Fatal(err)
	}
	if !report.Quiescent || report.AddedFaults != (rpc.DeliveryFaults{}) || report.After.InFlight != 0 {
		t.Fatal(report)
	}
	for _, b := range []rpc.ByteStats{report.MemoryAfter.Receive, report.MemoryAfter.Send, report.MemoryAfter.Encoding, report.MemoryAfter.ControlReceive, report.MemoryAfter.ControlSend} {
		if b.Used != 0 {
			t.Fatal("retained frame budget", b)
		}
	}
}

type staticRPCSource struct{ endpoint rpc.Endpoint }

func (s staticRPCSource) Snapshot() discovery.Snapshot {
	return discovery.Snapshot{Ready: true, Revision: 1, ClusterID: 1, Endpoints: []rpc.Endpoint{s.endpoint}}
}

func TestKanaRPCOverloadDoesNotInvokeOrReplay(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s, c, addr := loopbackRPC(t, extractorFunc(func(ctx context.Context, text string) ([]d.Claim, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return Extract(ctx, text)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}), 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	first := make(chan error, 1)
	go func() {
		var out Response
		first <- c.Call(context.Background(), MethodAnalyze, Request{Text: "tech: go"}, &out)
	}()
	<-started
	second := make(chan error, 1)
	go func() {
		var out Response
		second <- c.Call(context.Background(), MethodAnalyze, Request{Text: "tech: go"}, &out)
	}()
	awaitRPC(t, func() bool { return s.Stats().Queued == 1 })
	pool, err := routing.New(context.Background(), staticRPCSource{rpc.Endpoint{ID: "loopback", Address: addr.String(), Transport: rpc.TCP}}, routing.Config{Transport: rpc.TCP, Dial: rpc.DialOptions{Client: rpc.ClientConfig{Timeout: time.Second}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := pool.Wait(ctx); err != nil {
			t.Error(err)
		}
		if stats := pool.MemoryStats(); stats.Accounts != 0 || stats.Total.Used != 0 {
			t.Error("pool budget retained", stats)
		}
	}()
	client := &RPCClient{client: pool, sem: make(chan struct{}, 1), timeout: time.Second}
	_, err = client.Call(context.Background(), Request{Text: "tech: go"})
	var overload *rpc.Status
	if InvocationRejected(err) || !errors.As(err, &overload) || overload.Code != rpc.ResourceExhausted || calls.Load() != 1 {
		t.Fatal("overload execution/replay", err, calls.Load())
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected business replay", calls.Load())
	}
}

func TestKanaRPCRefusalClassification(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, rpc.ErrClosed, &rpc.Status{Code: rpc.ResourceExhausted, Message: "response budget"}, &rpc.Status{Code: rpc.Internal, Message: "failed"}, &rpc.Status{Code: rpc.Unavailable, Message: "transport failed"}, &rpc.Status{Code: rpc.ResourceExhausted}, &rpc.Status{Code: rpc.Unavailable}} {
		if rpcRefusedBeforeExecution(err) || InvocationRejected(err) {
			t.Fatal("uncertain outcome classified as safe", err)
		}
	}
	for _, err := range []error{&routing.AdmissionError{Kind: routing.ErrNotReady, Code: rpc.Unavailable}, &rpc.Status{Code: rpc.ResourceExhausted, Message: "client admission limit"}, &rpc.Status{Code: rpc.ResourceExhausted, Message: "local pressure admission"}} {
		if !rpcRefusedBeforeExecution(err) || !InvocationRejected(&invocationRefused{cause: err}) {
			t.Fatal("definitive refusal lost", err)
		}
	}
	for _, status := range []int{401, 403, 429} {
		if !InvocationRejected(&HTTPError{Status: status}) {
			t.Fatal(status)
		}
	}
	if InvocationRejected(&HTTPError{Status: 503}) {
		t.Fatal("provider may have executed")
	}
}

func TestKanaRPCLocalWaitNeverMarksInvocationUncertain(t *testing.T) {
	client := &RPCClient{sem: make(chan struct{}, 1), timeout: 10 * time.Millisecond}
	client.sem <- struct{}{}
	// A nil transport proves the timeout must finish before routing handoff.
	_, err := client.Call(context.Background(), Request{Text: "tech: go"})
	if !InvocationRejected(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Call(ctx, Request{})
	if !InvocationRejected(err) || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
