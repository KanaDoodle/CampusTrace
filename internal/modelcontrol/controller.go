// Package modelcontrol coordinates matching calls across API processes. Only
// hashed input identities and execution metadata are persisted; never messages,
// provider credentials or responses. The model itself remains in the caller.
package modelcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var ErrWaitDeadline = errors.New("global matching queue deadline exceeded before model call")

type Options struct {
	Policy                       p.ModelPoolPolicy
	Wait, Execution, Grace, Poll time.Duration
}
type Controller struct {
	Store   *p.Store
	Options Options
}

func New(ctx context.Context, s *p.Store, opts Options) (*Controller, error) {
	if opts.Wait <= 0 || opts.Wait > 10*time.Minute || opts.Execution <= 0 || opts.Execution > 5*time.Minute || opts.Grace < 0 || opts.Grace > time.Minute {
		return nil, p.ErrValidation
	}
	if opts.Poll == 0 {
		opts.Poll = 250 * time.Millisecond
		if v := time.Duration(opts.Policy.LeaseMS) * time.Millisecond / 3; v < opts.Poll {
			opts.Poll = v
		}
	}
	if opts.Poll < time.Millisecond || opts.Poll >= time.Duration(opts.Policy.LeaseMS)*time.Millisecond {
		return nil, p.ErrValidation
	}
	if e := s.EnsureModelPool(ctx, opts.Policy); e != nil {
		return nil, e
	}
	return &Controller{Store: s, Options: opts}, nil
}
func persistenceError(e error) error {
	for _, known := range []error{p.ErrModelQueueFull, p.ErrModelLease, p.ErrModelBudget, p.ErrModelReview, p.ErrMatchQuota, p.ErrMatchRunLease, p.ErrValidation} {
		if errors.Is(e, known) {
			return known
		}
	}
	return p.ErrBackendUnavailable
}
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}
func (c *Controller) Complete(ctx context.Context, user, identity string, model resume.Completer, messages, tools any, started func(), checks ...func(context.Context) error) (result json.RawMessage, err error) {
	ctx, span := observability.Start(ctx, "model.control")
	defer func() { observability.End(span, err) }()
	fingerprint := d.Hash(identity + "\n" + d.JSON(messages) + "\n" + d.JSON(tools))
	if e := c.Store.CheckModelCallReview(ctx, user, c.Options.Policy.ID, fingerprint); e != nil {
		return nil, persistenceError(e)
	}
	ctx, waiting := observability.Start(ctx, "model.queue_wait")
	defer waiting.End()
	waitStarted := time.Now()
	ticket, e := c.Store.EnqueueModelTicket(ctx, c.Options.Policy.ID, user, c.Options.Wait)
	if e != nil {
		return nil, persistenceError(e)
	}
	defer func() {
		cleanup, cancel := detached(ctx)
		defer cancel()
		_ = c.Store.CancelModelTicket(cleanup, ticket)
	}()
	if e = c.Store.ModelRunPhase(ctx, user, "QUEUED", ticket.WaitUntil); e != nil {
		return nil, persistenceError(e)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, c.Options.Wait)
	defer cancelWait()
	for {
		if waitCtx.Err() != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrWaitDeadline
		}
		ticket, e = c.Store.PollModelTicket(waitCtx, ticket.ID, user)
		if e != nil {
			if waitCtx.Err() != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, ErrWaitDeadline
			}
			return nil, persistenceError(e)
		}
		if ticket.State == "GRANTED" {
			break
		}
		if ticket.State != "QUEUED" {
			return nil, ErrWaitDeadline
		}
		timer := time.NewTimer(c.Options.Poll)
		select {
		case <-waitCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	cancelWait()
	waiting.End()
	// Subsequent work is a sibling of the wait span, not its child.
	ctx = trace.ContextWithSpan(ctx, span)
	if c.Store.Metrics != nil {
		c.Store.Metrics.Observe("model_queue_seconds", time.Since(waitStarted).Seconds())
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// The request deadline begins after the global wait. A stricter parent
	// deadline still wins, and publication keeps the original MatchRun guard.
	callCtx, cancelCall := context.WithTimeout(ctx, c.Options.Execution)
	defer cancelCall()
	until, _ := callCtx.Deadline()
	if e = c.Store.ModelRunPhase(ctx, user, "EXECUTING", until); e != nil {
		return nil, persistenceError(e)
	}
	for _, check := range checks {
		if check != nil {
			if err := check(ctx); err != nil {
				return nil, err
			}
		}
	}
	call, e := c.Store.BeginModelCall(ctx, ticket, fingerprint, d.Hash(identity), observability.From(ctx).RunID, c.Options.Execution, c.Options.Grace)
	if e != nil {
		return nil, persistenceError(e)
	}
	_ = call
	if c.Store.Metrics != nil {
		c.Store.Metrics.Add("model_control_calls_total", 1)
	}
	if started != nil {
		started()
	}
	if callCtx.Err() != nil {
		cleanup, cancel := detached(ctx)
		defer cancel()
		_ = c.Store.FinishModelCall(cleanup, ticket, "NOT_SENT", "CANCELLED_BEFORE_SEND")
		return nil, callCtx.Err()
	}
	var lost atomic.Bool
	hbCtx, stopHB := context.WithCancel(callCtx)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		ticker := time.NewTicker(time.Duration(c.Options.Policy.LeaseMS) * time.Millisecond / 3)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(hbCtx, time.Duration(c.Options.Policy.LeaseMS)*time.Millisecond/3)
				e := c.Store.RenewModelCall(renewCtx, ticket)
				cancel()
				if e != nil {
					if hbCtx.Err() == nil {
						lost.Store(true)
						cancelCall()
					}
					return
				}
			}
		}
	}()
	var raw json.RawMessage
	callCtx, providerSpan := observability.Client(callCtx, "model.provider")
	if m, ok := model.(interface {
		CompleteJSON(context.Context, any, any) (json.RawMessage, error)
	}); ok {
		raw, e = m.CompleteJSON(callCtx, messages, tools)
	} else {
		raw, e = model.Complete(callCtx, messages, tools)
	}
	callContextErr := callCtx.Err()
	providerErr := e
	if providerErr == nil {
		providerErr = callContextErr
	}
	observability.End(providerSpan, providerErr)
	stopHB()
	<-hbDone
	state, code := "RESPONSE_RECEIVED", "RESPONSE_RECEIVED"
	if e != nil || callContextErr != nil {
		state, code = "UNKNOWN", "PROVIDER_OUTCOME_UNKNOWN"
		var rejected *analysis.HTTPError
		var invalid *analysis.ResponseError
		if errors.As(e, &rejected) && (rejected.Status == 400 || rejected.Status == 401 || rejected.Status == 402 || rejected.Status == 403 || rejected.Status == 429) {
			state, code = "REJECTED", "PROVIDER_REJECTED"
		}
		if errors.As(e, &invalid) {
			state, code = "RESPONSE_RECEIVED", "INVALID_RESPONSE"
		}
	}
	cleanup, cancel := detached(ctx)
	span.SetAttributes(attribute.String("model.call.outcome", state))
	if state == "UNKNOWN" && c.Store.Metrics != nil {
		c.Store.Metrics.Add("model_control_unknown_total", 1)
	}
	finishErr := c.Store.FinishModelCall(cleanup, ticket, state, code)
	cancel()
	if finishErr != nil {
		return nil, persistenceError(finishErr)
	}
	if lost.Load() {
		return nil, p.ErrModelLease
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if callContextErr != nil {
		return nil, callContextErr
	}
	return raw, e
}
