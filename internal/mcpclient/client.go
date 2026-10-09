// Package mcpclient consumes explicitly selected textual MCP resources only.
// It never calls remote tools, executes commands, or grants a server credentials
// for another origin. Remote content remains untrusted reference material.
package mcpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Connector struct {
	ID      string            `json:"id,omitempty"`
	Version uint64            `json:"version,omitempty"`
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Allowed []string          `json:"allowed_resources"`
	Names   map[string]string `json:"resource_names,omitempty"`
}

func (c Connector) Validate() error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 120 || len(c.Allowed) > 50 {
		return errors.New("invalid connector")
	}
	if e := (modelconfig.Config{URL: c.URL, Model: "mcp-resource-client", APIKey: "unused"}).Validate(); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, s := range c.Allowed {
		if s == "" || len(s) > 2048 || strings.ContainsAny(s, "\r\n\x00") || seen[s] {
			return errors.New("invalid resource selection")
		}
		seen[s] = true
	}
	for uri, name := range c.Names {
		if !seen[uri] || len(name) > 200 || strings.ContainsAny(name, "\r\n\x00") {
			return errors.New("invalid resource name")
		}
	}
	return nil
}

type Resource struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
	MIME string `json:"mime_type,omitempty"`
}
type Document struct {
	URI    string `json:"uri"`
	Text   string `json:"text"`
	Source string `json:"source"`
}
type Client struct{ HTTP *http.Client }
type boundedTransport struct {
	next   http.RoundTripper
	token  string
	origin string
}

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme+"://"+r.URL.Host != t.origin {
		return nil, modelconfig.ErrInvalid
	}
	r = r.Clone(r.Context())
	if t.token != "" {
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	v, e := t.next.RoundTrip(r)
	if e != nil {
		return nil, e
	}
	if v.ContentLength > 1<<20 {
		v.Body.Close()
		return nil, errors.New("MCP response too large")
	}
	v.Body = &limitedBody{Reader: io.LimitReader(v.Body, (1<<20)+1), Closer: v.Body}
	return v, nil
}

type limitedBody struct {
	io.Reader
	io.Closer
}

func (c Client) session(ctx context.Context, cfg Connector, token string) (*mcp.ClientSession, error) {
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	if len(token) > 2048 || strings.ContainsAny(token, "\r\n\x00") {
		return nil, errors.New("invalid credential")
	}
	h := c.HTTP
	if h == nil {
		h = modelconfig.PublicClient()
	}
	copy := *h
	next := copy.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	// Config validation fixes a single HTTPS origin; public DNS/IP validation is
	// repeated at every connection by PublicClient, with proxying disabled.
	endpoint, _ := url.Parse(cfg.URL)
	copy.Transport = boundedTransport{next, token, endpoint.Scheme + "://" + endpoint.Host}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return modelconfig.ErrInvalid }
	cli := mcp.NewClient(&mcp.Implementation{Name: "CampusTrace resource client", Version: "1"}, nil)
	return cli.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: &copy, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
}
func (c Client) Discover(ctx context.Context, cfg Connector, token string) ([]Resource, error) {
	s, e := c.session(ctx, cfg, token)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	out := []Resource{}
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 5 {
			return nil, errors.New("MCP resource catalog too large")
		}
		v, e := s.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if e != nil {
			return nil, e
		}
		for _, r := range v.Resources {
			if len(out) >= 100 {
				return nil, errors.New("MCP resource catalog too large")
			}
			if r != nil && len(r.URI) <= 2048 && len(r.Name) <= 200 {
				out = append(out, Resource{r.URI, r.Name, r.MIMEType})
			}
		}
		if v.NextCursor == "" {
			break
		}
		cursor = v.NextCursor
	}
	return out, nil
}
func (c Client) Read(ctx context.Context, cfg Connector, token, uri string) (Document, error) {
	allowed := false
	for _, u := range cfg.Allowed {
		allowed = allowed || u == uri
	}
	if !allowed {
		return Document{}, errors.New("resource not authorized")
	}
	s, e := c.session(ctx, cfg, token)
	if e != nil {
		return Document{}, e
	}
	defer s.Close()
	v, e := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if e != nil {
		return Document{}, e
	}
	text := ""
	for _, r := range v.Contents {
		if r == nil || r.URI != uri || len(r.Blob) > 0 || r.MIMEType != "" && !strings.HasPrefix(r.MIMEType, "text/") {
			return Document{}, errors.New("only selected text resources are supported")
		}
		text += r.Text + "\n"
		if len(text) > 24000 {
			return Document{}, errors.New("MCP document exceeds context bound")
		}
	}
	return Document{uri, text, cfg.Name}, nil
}
