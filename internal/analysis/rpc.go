package analysis

import (
	"context"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	rpc "github.com/KanaDoodle/KanaRPC"
	"github.com/KanaDoodle/KanaRPC/discovery"
	"github.com/KanaDoodle/KanaRPC/routing"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	ServiceName         = "CampusTraceAnalysis"
	MethodAnalyze       = "/campustrace.analysis.v1/AnalyzeObservation"
	DefaultRPCNamespace = "/campustrace/kanarpc/v2"
)

// The KNR2 protocol uses its own discovery namespace. Legacy nodes cannot be
// selected by a new worker during a coordinated deployment.
func RPCNamespace() string {
	if value := os.Getenv("ANALYSIS_RPC_NAMESPACE"); value != "" {
		return strings.TrimSuffix(value, "/")
	}
	return DefaultRPCNamespace
}

func serviceName(names []string) string {
	if len(names) > 0 {
		return names[0]
	}
	return ServiceName
}

var ErrSemanticVersion = errors.New("schema: incompatible analysis semantics")

type Request struct {
	AnalysisVersion string    `json:"analysis_version,omitempty"`
	TraceParent     string    `json:"traceparent,omitempty"`
	TaskID          string    `json:"task_id"`
	JobID           string    `json:"job_id"`
	ObservationID   string    `json:"observation_id"`
	Text            string    `json:"text"`
	Deadline        time.Time `json:"deadline"`
	CorrelationID   string    `json:"correlation_id"`
}
type Response struct {
	AnalysisVersion string    `json:"analysis_version"`
	Claims          []d.Claim `json:"claims"`
	Instance        string    `json:"instance"`
	Error           string    `json:"error,omitempty"`
	Category        string    `json:"category,omitempty"`
	RejectedStatus  int       `json:"rejected_status,omitempty"`
}
type Extractor interface {
	Claims(context.Context, string) ([]d.Claim, error)
}
type RuleExtractor struct{}

func (RuleExtractor) Claims(ctx context.Context, text string) ([]d.Claim, error) {
	return Extract(ctx, text)
}

type Service struct {
	Root      context.Context
	Instance  string
	Extractor Extractor
	Delay     time.Duration
}

func (s *Service) AnalyzeObservation(ctx context.Context, req Request) (resp Response, err error) {
	deadline := req.Deadline
	if deadline.IsZero() || deadline.After(time.Now().Add(30*time.Second)) {
		deadline = time.Now().Add(30 * time.Second)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if s.Root != nil {
		stop := context.AfterFunc(s.Root, cancel)
		defer stop()
	}
	ctx, span := observability.Server(observability.Extract(ctx, req.TraceParent), "rpc.AnalyzeObservation")
	defer func() {
		if resp.Error != "" {
			span.SetStatus(codes.Error, "analysis rejected")
		}
		observability.End(span, err)
	}()
	ctx = observability.With(ctx, observability.Fields{RequestID: req.CorrelationID, TaskID: req.TaskID, JobID: req.JobID})
	span.SetAttributes(attribute.String("rpc.system", "kanarpc"), attribute.String("rpc.method", "AnalyzeObservation"), attribute.String("task.id", req.TaskID))
	resp.Instance = s.Instance
	resp.AnalysisVersion = d.AnalysisVersion
	if req.AnalysisVersion != "" && req.AnalysisVersion != d.AnalysisVersion {
		resp.Error, resp.Category = "analysis version mismatch", "SCHEMA"
		return resp, nil
	}
	if s.Delay > 0 {
		timer := time.NewTimer(s.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return resp, err
	}
	claims, err := s.Extractor.Claims(ctx, req.Text)
	if err != nil {
		if ctx.Err() != nil {
			return resp, ctx.Err()
		}
		resp.Error = "analysis failed"
		resp.Category = "SCHEMA"
		var rejected *HTTPError
		if errors.As(err, &rejected) && (rejected.Status == 429 || rejected.Status == 401 || rejected.Status == 403) {
			resp.RejectedStatus = rejected.Status
		}
		if IsTransient(err) {
			resp.Category = "TRANSIENT"
		}
		return resp, nil
	}
	resp.Claims = claims
	slog.InfoContext(ctx, "analysis completed", "observation_id", req.ObservationID, "request_id", req.CorrelationID, "rpc_method", "AnalyzeObservation", "instance", s.Instance, "task_id", req.TaskID, "job_id", req.JobID)
	return resp, nil
}
func IsTransient(err error) bool {
	var n net.Error
	var h *HTTPError
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &n) || (errors.As(err, &h) && (h.Status == 429 || h.Status >= 500))
}

// RPCClient owns discovery and a bounded connection pool. A business call is
// dispatched once; Kafka/SQL retain ownership of task retries and receipts.
type RPCClient struct {
	client  *routing.Client
	reg     *discovery.Registry
	source  *analysisDiscovery
	cancel  context.CancelFunc
	once    sync.Once
	sem     chan struct{}
	timeout time.Duration
}

func NewClient(endpoints []string, limit int, timeout time.Duration, names ...string) (*RPCClient, error) {
	if limit < 1 || limit > 1024 || timeout <= 0 {
		return nil, errors.New("analysis: invalid RPC client bounds")
	}
	root, cancel := context.WithCancel(context.Background())
	r, err := discovery.New(root, discovery.Config{Endpoints: endpoints, Namespace: RPCNamespace(), MaxResolvers: 2})
	if err != nil {
		cancel()
		return nil, err
	}
	fail := func() {
		cancel()
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = r.Close(ctx)
	}
	source, err := newAnalysisDiscovery(root, r, serviceName(names))
	if err != nil {
		fail()
		return nil, err
	}
	c, err := routing.New(root, source, routing.Config{
		Transport: rpc.TCP, MaxConnections: 8, MaxInFlight: limit,
		Dial: rpc.DialOptions{Client: rpc.ClientConfig{Timeout: timeout, MaxInFlight: max(32, limit), Feedback: &rpc.FeedbackConfig{}}},
	})
	if err != nil {
		fail()
		return nil, err
	}
	return &RPCClient{client: c, reg: r, source: source, cancel: cancel, sem: make(chan struct{}, limit), timeout: timeout}, nil
}

// Stats exposes bounded pool accounting for health diagnostics and acceptance.
func (c *RPCClient) Stats() routing.Stats { return c.client.Stats() }

// invocationRefused is created only for a refusal with proof that the analysis
// handler was not entered. Generic timeouts and transport errors stay uncertain.
type invocationRefused struct{ cause error }

func (e *invocationRefused) Error() string {
	return "503: analysis RPC admission rejected: " + e.cause.Error()
}
func (e *invocationRefused) Unwrap() error { return e.cause }

func rpcRefusedBeforeExecution(err error) bool {
	var admission *routing.AdmissionError
	if errors.As(err, &admission) {
		return true
	}
	var status *rpc.Status
	if !errors.As(err, &status) {
		return false
	}
	// Only local pre-send statuses are classified here. Remote rejections carry
	// no execution receipt; code-only overload/drain responses remain uncertain.
	if status.Code == rpc.ResourceExhausted {
		switch status.Message {
		case "client in-flight limit", "client admission limit", "local pressure admission":
			return true
		}
	}
	return false
}

// Fixed diagnostic labels avoid logging requests, model content or addresses.
func rpcFailureReason(err error) string {
	for _, entry := range []struct {
		err   error
		label string
	}{
		{routing.ErrNotReady, "discovery_not_ready"}, {routing.ErrSnapshot, "discovery_snapshot"},
		{routing.ErrCapacity, "routing_capacity"}, {routing.ErrBackoff, "dial_backoff"}, {routing.ErrRetired, "endpoint_retired"},
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline"},
	} {
		if errors.Is(err, entry.err) {
			return entry.label
		}
	}
	var status *rpc.Status
	if errors.As(err, &status) {
		return string(status.Code)
	}
	return "transport"
}

// InvocationRejected is also used by SQL invocation fencing. Only definitive
// provider refusals or proven RPC admission refusals allow another paid attempt.
func InvocationRejected(err error) bool {
	var admission *invocationRefused
	if errors.As(err, &admission) {
		return true
	}
	var provider *HTTPError
	return errors.As(err, &provider) && (provider.Status == 429 || provider.Status == 401 || provider.Status == 403)
}

func (c *RPCClient) Analyze(ctx context.Context, o d.Observation, correlation string) ([]d.Claim, error) {
	r, err := c.Call(ctx, Request{ObservationID: o.ID, Text: o.Text, CorrelationID: correlation, TaskID: observability.From(ctx).TaskID, JobID: o.JobID})
	return r.Claims, err
}
func (c *RPCClient) Call(ctx context.Context, req Request) (out Response, err error) {
	ctx, span := observability.Client(ctx, "rpc.AnalyzeObservation")
	defer func() { observability.End(span, err) }()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return out, &invocationRefused{cause: err}
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return out, &invocationRefused{cause: ctx.Err()}
	}
	if err := ctx.Err(); err != nil {
		return out, &invocationRefused{cause: err}
	}
	req.Deadline, _ = ctx.Deadline()
	req.AnalysisVersion = d.AnalysisVersion
	req.TraceParent = observability.TraceParent(ctx)
	err = c.client.Call(ctx, MethodAnalyze, req, &out)
	if rpcRefusedBeforeExecution(err) {
		err = &invocationRefused{cause: err}
	}
	if err != nil {
		stats := c.client.Stats()
		slog.WarnContext(ctx, "analysis RPC call failed", "reason", rpcFailureReason(err), "discovery_ready", stats.Ready, "endpoints", stats.Endpoints, "snapshot_errors", stats.SnapshotErrors)
		return out, err
	}
	if out.Error != "" {
		if out.RejectedStatus == 429 || out.RejectedStatus == 401 || out.RejectedStatus == 403 {
			return out, &HTTPError{Status: out.RejectedStatus}
		}
		if out.Category == "TRANSIENT" {
			return out, errors.New("503: analysis transient failure")
		}
		return out, errors.New("schema: analysis rejected")
	}
	return out, validateResponse(req, out)
}

func validateResponse(req Request, out Response) error {
	// Missing versions include pre-upgrade servers. Do not stamp their claims
	// with the caller's newer cache/receipt namespace during a rolling deploy.
	if out.AnalysisVersion != d.AnalysisVersion {
		return ErrSemanticVersion
	}
	for _, claim := range out.Claims {
		if err := claim.Validate(req.Text); err != nil {
			return err
		}
	}
	return nil
}
func (c *RPCClient) Close() {
	c.once.Do(func() {
		c.cancel()
		c.client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := c.client.Wait(ctx); err != nil {
			slog.Warn("analysis RPC pool cleanup incomplete", "error", err)
		}
		select {
		case <-c.source.done:
		case <-ctx.Done():
			slog.Warn("analysis discovery refresh cleanup incomplete")
		}
		if err := c.reg.Close(ctx); err != nil {
			slog.Warn("analysis discovery cleanup incomplete", "error", err)
		}
	})
}

type RunningServer struct {
	root         context.Context
	Server       *rpc.Server
	Registry     *discovery.Registry
	Registration *discovery.Registration
	listener     net.Listener
	key          string
	Cancel       context.CancelFunc
	Done         chan error
	stopped      chan struct{}
	closing      atomic.Bool
	closeOnce    sync.Once
	closeReport  rpc.DrainReport
	closeErr     error
}

func newRPCServer(service *Service, limit int) (*rpc.Server, error) {
	if limit < 1 || limit > 1024 {
		return nil, errors.New("analysis: invalid RPC server concurrency")
	}
	capacity := max(32, limit*4)
	s, err := rpc.NewServer(rpc.ServerConfig{
		MaxConnections: 64, MaxInFlight: capacity, MaxPerConnection: capacity,
		MaxRequestDuration: 30 * time.Second,
		Governance: &rpc.GovernanceConfig{MaxExecuting: limit, QueueCapacity: limit, QueueTimeout: 100 * time.Millisecond,
			QueuePolicy: rpc.MethodFair, Feedback: true, Methods: []rpc.MethodPolicy{{Method: MethodAnalyze, Max: limit}}},
	})
	if err != nil {
		return nil, err
	}
	if err = rpc.RegisterUnary(s, MethodAnalyze, service.AnalyzeObservation); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func StartServer(parent context.Context, endpoints []string, addr, advertise, instance string, extractor Extractor, limit int, delay time.Duration, names ...string) (*RunningServer, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if extractor == nil {
		return nil, errors.New("analysis: extractor required")
	}
	// Process signals initiate drain in Close rather than canceling active work.
	root, cancel := context.WithCancel(context.WithoutCancel(parent))
	s, err := newRPCServer(&Service{Root: root, Instance: instance, Extractor: extractor, Delay: delay}, limit)
	if err != nil {
		cancel()
		return nil, err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		cancel()
		s.Close()
		return nil, err
	}
	r, err := discovery.New(root, discovery.Config{Endpoints: endpoints, Namespace: RPCNamespace()})
	if err != nil {
		cancel()
		_ = listener.Close()
		s.Close()
		return nil, err
	}
	run := &RunningServer{root: root, Server: s, Registry: r, listener: listener, Cancel: cancel, Done: make(chan error, 1), stopped: make(chan struct{})}
	go func() { defer close(run.stopped); run.Done <- s.Serve(listener) }()
	if advertise == "" {
		advertise = listener.Addr().String()
	}
	name := serviceName(names)
	run.key = RPCNamespace() + "/services/" + name + "/" + instance
	registration, err := r.Register(parent, name, rpc.Endpoint{ID: instance, Address: advertise, Transport: rpc.TCP}, 10)
	if err != nil {
		run.Close()
		return nil, err
	}
	run.Registration = registration
	// Serve owns listener initialization. Do not advertise readiness before it.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	startup, stop := context.WithTimeout(parent, 5*time.Second)
	defer stop()
	for !run.Ready() {
		select {
		case err := <-run.Done:
			run.Close()
			if err == nil {
				err = rpc.ErrClosed
			}
			return nil, fmt.Errorf("analysis RPC listener stopped: %w", err)
		case <-startup.Done():
			run.Close()
			return nil, startup.Err()
		case <-ticker.C:
		}
	}
	return run, nil
}

func (r *RunningServer) Addr() net.Addr          { return r.listener.Addr() }
func (r *RunningServer) RegistrationKey() string { return r.key }

// Shutdown withdraws discovery first, then joins accepted RPCs and terminal
// receipts. A deadline forces cancellation; the report retains delivery faults.
func (r *RunningServer) Shutdown(ctx context.Context) (rpc.DrainReport, error) {
	r.closeOnce.Do(func() {
		r.closing.Store(true)
		var withdraw error
		if r.Registration != nil {
			withdraw = r.Registration.Close(ctx)
		}
		r.closeReport, r.closeErr = r.Server.DrainWithReport(ctx)
		_ = r.listener.Close() // also covers Serve failing before listener ownership
		r.Cancel()
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		r.closeErr = errors.Join(withdraw, r.closeErr, r.Registry.Close(cleanup))
		select {
		case <-r.stopped:
		case <-cleanup.Done():
			r.closeErr = errors.Join(r.closeErr, cleanup.Err())
		}
	})
	return r.closeReport, r.closeErr
}
func (r *RunningServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := r.Shutdown(ctx)
	if err != nil {
		slog.Warn("analysis RPC shutdown outcome", "error", err, "quiescent", report.Quiescent, "lost_terminals", report.TotalFaults.LostTerminals, "incomplete_exchanges", report.TotalFaults.IncompleteExchanges)
	}
}
func (r *RunningServer) Ready() bool {
	return r != nil && !r.closing.Load() && r.root.Err() == nil && r.Server.Ready() && r.Registration != nil && r.Registration.State().Ready
}
