package mcpclient

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type bridge struct {
	next   http.RoundTripper
	target *url.URL
	seen   *atomic.Int32
}

func (b bridge) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") != "Bearer synthetic-token" {
		panic("missing credential")
	}
	b.seen.Add(1)
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = b.target.Scheme
	u.Host = b.target.Host
	copy.URL = &u
	copy.Host = b.target.Host
	return b.next.RoundTrip(copy)
}
func TestRealMCPResourceHandshakeAndReadBoundaries(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "synthetic", Version: "1"}, nil)
	var reads atomic.Int32
	for _, v := range []struct{ uri, mime, text string }{{"learn://go", "text/plain", "Go并发复习"}, {"learn://binary", "image/png", "not text"}, {"learn://large", "text/plain", strings.Repeat("a", 24001)}} {
		item := v
		server.AddResource(&mcp.Resource{URI: item.uri, Name: item.uri, MIMEType: item.mime}, func(ctx context.Context, r *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			reads.Add(1)
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: item.uri, MIMEType: item.mime, Text: item.text}}}, nil
		})
	}
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true, Stateless: true}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	var seen atomic.Int32
	client := Client{HTTP: &http.Client{Transport: bridge{http.DefaultTransport, u, &seen}, Timeout: time.Second * 5}}
	cfg := Connector{Name: "学习", URL: "https://example.com/mcp", Allowed: []string{"learn://go", "learn://binary", "learn://large"}}
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	catalog, e := client.Discover(ctx, cfg, "synthetic-token")
	if e != nil || len(catalog) != 3 {
		t.Fatal(catalog, e)
	}
	v, e := client.Read(ctx, cfg, "synthetic-token", "learn://go")
	if e != nil || !strings.Contains(v.Text, "Go并发") {
		t.Fatal(v, e)
	}
	before := seen.Load()
	if _, e = client.Read(ctx, cfg, "synthetic-token", "file:///unselected"); e == nil || seen.Load() != before {
		t.Fatal("unselected resource contacted server")
	}
	for _, uri := range []string{"learn://binary", "learn://large"} {
		if _, e = client.Read(ctx, cfg, "synthetic-token", uri); e == nil {
			t.Fatal("accepted unsupported resource", uri)
		}
	}
	if reads.Load() != 3 {
		t.Fatal(reads.Load())
	}
}

type oversized struct{}

func (oversized) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, ContentLength: 1 << 21, Body: io.NopCloser(strings.NewReader("oversize"))}, nil
}
func TestMCPRejectsPrivateEndpointsAndCrossOrigin(t *testing.T) {
	for _, u := range []string{"http://example.com/mcp", "https://127.0.0.1/mcp", "https://localhost/mcp", "https://example.com/mcp?secret=1"} {
		if (Connector{Name: "test", URL: u}).Validate() == nil {
			t.Fatal(u)
		}
	}
	tr := boundedTransport{next: oversized{}, origin: "https://example.com"}
	r, _ := http.NewRequest("GET", "https://other.example/mcp", nil)
	if _, e := tr.RoundTrip(r); e == nil {
		t.Fatal("cross origin")
	}
	r, _ = http.NewRequest("GET", "https://example.com/mcp", nil)
	if _, e := tr.RoundTrip(r); e == nil {
		t.Fatal("oversize")
	}
}
