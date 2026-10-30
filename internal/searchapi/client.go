package searchapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/observability"
)

type Client struct {
	URL, QueryToken, WriteToken string
	HTTP                        *http.Client
	guard                       *queryGuard
}

func NewClient(endpoint, queryToken, writeToken string) (*Client, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || len(queryToken) < 32 || writeToken != "" && (len(writeToken) < 32 || queryToken == writeToken) {
		return nil, ErrInvalid
	}
	return &Client{URL: strings.TrimRight(endpoint, "/"), QueryToken: queryToken, WriteToken: writeToken, guard: newQueryGuard(DefaultQueryPolicy(), nil), HTTP: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) call(ctx context.Context, path, token string, input, output any) (err error) {
	ctx, span := observability.Client(ctx, "search.http")
	defer func() { observability.End(span, err) }()
	method := "GET"
	var body io.Reader
	if input != nil {
		raw, e := json.Marshal(input)
		if e != nil {
			return ErrInvalid
		}
		body = bytes.NewReader(raw)
		method = "POST"
	}
	req, e := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if e != nil {
		return ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if parent := observability.TraceParent(ctx); parent != "" {
		req.Header.Set("traceparent", parent)
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 400 {
		return ErrInvalid
	}
	if resp.StatusCode == 409 {
		return ErrConflict
	}
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return ErrUnavailable
	}
	if output != nil && json.Unmarshal(raw, output) != nil {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) Status(ctx context.Context) (out Status, err error) {
	err = c.call(ctx, "/v1/status", c.QueryToken, nil, &out)
	if err == nil && (out.Protocol != Protocol || out.Generation == "") {
		err = ErrUnavailable
	}
	return
}

// Split by encoded size as well as count: escaped JD text can be much larger
// on the wire. A partial failure leaves the whole Core batch unacknowledged.
func (c *Client) Apply(ctx context.Context, b Batch) error {
	if b.Protocol != Protocol || len(b.Documents) == 0 || len(b.Documents) > MaxBatch {
		return ErrInvalid
	}
	for start := 0; start < len(b.Documents); {
		end, size := start, 512
		for end < len(b.Documents) {
			raw, e := json.Marshal(b.Documents[end])
			if e != nil || len(raw) > 1<<20 {
				return ErrInvalid
			}
			if size+len(raw) > 4<<20 {
				break
			}
			size += len(raw) + 1
			end++
		}
		var out Status
		if e := c.call(ctx, "/v1/batch", c.WriteToken, Batch{Protocol: b.Protocol, Generation: b.Generation, Documents: b.Documents[start:end]}, &out); e != nil {
			return e
		}
		if out.Protocol != Protocol || out.Generation != b.Generation {
			return ErrConflict
		}
		start = end
	}
	return nil
}

// ConfigureQueries is a startup-only operation, before sharing this client.
func (c *Client) ConfigureQueries(policy QueryPolicy, metrics *observability.Metrics) error {
	if !policy.Valid() {
		return ErrInvalid
	}
	c.guard = newQueryGuard(policy, metrics)
	return nil
}
func (c *Client) QueryState() QueryState {
	if c.guard == nil {
		return QueryState{State: "unmanaged"}
	}
	return c.guard.snapshot()
}
func (c *Client) Query(ctx context.Context, q Query) (out Result, err error) {
	call := func(ctx context.Context) error {
		e := c.call(ctx, "/v1/query", c.QueryToken, q, &out)
		if e == nil && (out.Protocol != Protocol || out.Generation != q.Generation || len(out.Hits) > q.Limit) {
			return ErrConflict
		}
		return e
	}
	if c.guard == nil {
		err = call(ctx)
	} else {
		err = c.guard.run(ctx, call)
	}
	return
}

func (c *Client) Rebuild(ctx context.Context) (out Status, err error) {
	err = c.call(ctx, "/v1/rebuild", c.WriteToken, struct{}{}, &out)
	return
}

func (c *Client) Applied(ctx context.Context, q AppliedQuery) (AppliedResult, error) {
	var out AppliedResult
	err := c.call(ctx, "/v1/applied", c.QueryToken, q, &out)
	if err == nil && (out.Protocol != Protocol || out.Generation != q.Generation) {
		err = ErrConflict
	}
	return out, err
}
