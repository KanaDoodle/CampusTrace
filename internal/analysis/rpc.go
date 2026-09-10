package analysis

import (
	"context"
	"errors"
	"github.com/KanaDoodle/KanaRPC-Go/rpc"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"log/slog"
	"net"
	"time"
)

const ServiceName = "CampusTraceAnalysis"

type Request struct {
	TaskID        string    `json:"task_id"`
	JobID         string    `json:"job_id"`
	ObservationID string    `json:"observation_id"`
	Text          string    `json:"text"`
	Deadline      time.Time `json:"deadline"`
	CorrelationID string    `json:"correlation_id"`
}
type Response struct {
	Claims   []d.Claim `json:"claims"`
	Instance string    `json:"instance"`
	Error    string    `json:"error,omitempty"`
	Category string    `json:"category,omitempty"`
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

func (s *Service) AnalyzeObservation(req *Request, resp *Response) error {
	// MyRPC only supports net/rpc-style methods. Explicit envelope deadline bounds
	// server work. Early remote cancellation is unavailable in its wire protocol.
	deadline := req.Deadline
	if deadline.IsZero() || deadline.After(time.Now().Add(30*time.Second)) {
		deadline = time.Now().Add(30 * time.Second)
	}
	ctx, cancel := context.WithDeadline(s.Root, deadline)
	defer cancel()
	resp.Instance = s.Instance
	if s.Delay > 0 {
		timer := time.NewTimer(s.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	claims, err := s.Extractor.Claims(ctx, req.Text)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp.Error = "analysis failed"
		resp.Category = "SCHEMA"
		if IsTransient(err) {
			resp.Category = "TRANSIENT"
		}
		return nil
	}
	resp.Claims = claims
	slog.InfoContext(ctx, "analysis completed", "observation_id", req.ObservationID, "request_id", req.CorrelationID, "rpc_method", "AnalyzeObservation", "instance", s.Instance, "task_id", req.TaskID, "job_id", req.JobID)
	return nil
}
func IsTransient(err error) bool {
	var n net.Error
	var h *HTTPError
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &n) || (errors.As(err, &h) && (h.Status == 429 || h.Status >= 500))
}

type RPCClient struct {
	client  *rpc.Client
	reg     *rpc.Registry
	sem     chan struct{}
	service string
	timeout time.Duration
}

func NewClient(endpoints []string, limit int, timeout time.Duration, names ...string) (*RPCClient, error) {
	r, err := rpc.NewRegistry(endpoints)
	if err != nil {
		return nil, err
	}
	c, err := rpc.NewClient(r, rpc.ClientConfig{Timeout: timeout})
	if err != nil {
		r.Close()
		return nil, err
	}
	name := ServiceName
	if len(names) > 0 {
		name = names[0]
	}
	return &RPCClient{client: c, reg: r, sem: make(chan struct{}, limit), service: name, timeout: timeout}, nil
}
func (c *RPCClient) Analyze(ctx context.Context, o d.Observation, correlation string) ([]d.Claim, error) {
	r, err := c.Call(ctx, Request{ObservationID: o.ID, Text: o.Text, CorrelationID: correlation, TaskID: observability.From(ctx).TaskID, JobID: o.JobID})
	return r.Claims, err
}
func (c *RPCClient) Call(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var out Response
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return out, ctx.Err()
	}
	req.Deadline, _ = ctx.Deadline()
	err := c.client.Invoke(ctx, c.service, "AnalyzeObservation", &req, &out)
	if err != nil {
		return out, err
	}
	if out.Error != "" {
		if out.Category == "TRANSIENT" {
			return out, errors.New("503: analysis transient failure")
		}
		return out, errors.New("schema: analysis rejected")
	}
	for _, v := range out.Claims {
		if err = v.Validate(req.Text); err != nil {
			return out, err
		}
	}
	return out, nil
}
func (c *RPCClient) Close() { c.client.Close(); c.reg.Close() }

type RunningServer struct {
	root     context.Context
	Server   *rpc.Server
	Registry *rpc.Registry
	Cancel   context.CancelFunc
	Done     chan error
}

func StartServer(parent context.Context, endpoints []string, addr, advertise, instance string, extractor Extractor, limit int, delay time.Duration, names ...string) (*RunningServer, error) {
	r, err := rpc.NewRegistry(endpoints)
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(parent)
	s, err := rpc.NewServer(rpc.ServerConfig{Address: addr, MaxConcurrentRequests: limit})
	if err != nil {
		cancel()
		r.Close()
		return nil, err
	}
	name := ServiceName
	if len(names) > 0 {
		name = names[0]
	}
	s.Register(name, &Service{Root: root, Instance: instance, Extractor: extractor, Delay: delay})
	run := &RunningServer{root: root, Server: s, Registry: r, Cancel: cancel, Done: make(chan error, 1)}
	go func() { err := s.Start(); cancel(); run.Done <- err }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for s.Addr() == nil {
		select {
		case err := <-run.Done:
			cancel()
			r.Close()
			return nil, err
		case <-parent.Done():
			run.Close()
			return nil, parent.Err()
		case <-ticker.C:
		}
	}
	if advertise == "" {
		advertise = s.Addr().String()
	}
	if err = r.Register(name, advertise, 10); err != nil {
		run.Close()
		return nil, err
	}
	return run, nil
}
func (r *RunningServer) Close() { r.Cancel(); r.Registry.Close(); r.Server.Shutdown() }

func (r *RunningServer) Ready() bool {
	return r != nil && r.root.Err() == nil && r.Server.Addr() != nil && r.Registry.Ready()
}
