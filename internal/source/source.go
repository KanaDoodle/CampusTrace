package source

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"golang.org/x/net/html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Result struct {
	Text, Status string
	HTTPStatus   int
}
type Adapter interface {
	Fetch(context.Context, string) (Result, error)
	Version() string
}
type HTTPAdapter struct{ Client *http.Client }

func PublicClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("no public address")
		}
		for _, v := range ips {
			if v.IP.IsPrivate() || v.IP.IsLoopback() || v.IP.IsLinkLocalUnicast() || v.IP.IsLinkLocalMulticast() || v.IP.IsUnspecified() || v.IP.IsMulticast() {
				return nil, errors.New("only public source addresses allowed")
			}
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 4 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
			return errors.New("unsupported scheme")
		}
		return nil
	}}
}
func (a HTTPAdapter) Version() string { return d.SourceParserVersion }
func (a HTTPAdapter) Fetch(ctx context.Context, raw string) (Result, error) {
	v, err := url.Parse(raw)
	if err != nil || v.Host == "" || (v.Scheme != "https" && v.Scheme != "http") || v.User != nil {
		return Result{}, errors.New("invalid public URL")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "CampusTrace/1.0 (manual public source verification)")
	resp, err := a.Client.Do(req)
	if err != nil {
		status := "HTTP_ERROR"
		var n net.Error
		if errors.As(err, &n) && n.Timeout() {
			status = "TIMEOUT"
		}
		return Result{Status: status}, nil
	}
	defer resp.Body.Close()
	r := Result{Status: "SUCCESS", HTTPStatus: resp.StatusCode}
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 {
		r.Status = "BLOCKED"
		return r, nil
	}
	if resp.StatusCode != 200 {
		r.Status = "HTTP_ERROR"
		return r, nil
	}
	media, _, mediaErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaErr != nil || !(strings.HasPrefix(media, "text/") || media == "application/xhtml+xml") {
		r.Status = "PARSE_ERROR"
		return r, nil
	}
	var body io.Reader = resp.Body
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "", "identity":
	case "gzip":
		gz, e := gzip.NewReader(resp.Body)
		if e != nil {
			r.Status = "PARSE_ERROR"
			return r, nil
		}
		defer gz.Close()
		body = gz
	default:
		r.Status = "PARSE_ERROR"
		return r, nil
	}
	b, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		r.Status = "PARSE_ERROR"
		return r, nil
	}
	low := strings.ToLower(string(b))
	for _, signal := range []string{"captcha", "sign in to continue", "login required", "access denied", "verify you are human"} {
		if strings.Contains(low, signal) {
			r.Status = "BLOCKED"
			return r, nil
		}
	}
	root, err := html.Parse(strings.NewReader(string(b)))
	if err != nil {
		r.Status = "PARSE_ERROR"
		return r, nil
	}
	parts := []string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		if n.Type == html.TextNode {
			v := strings.TrimSpace(n.Data)
			if v != "" {
				parts = append(parts, v)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	r.Text = strings.Join(parts, "\n")
	if len(r.Text) > 60000 || len(r.Text) == 0 {
		r.Status = "PARSE_ERROR"
		r.Text = ""
	}
	return r, nil
}
func CSV(r io.Reader) ([]p.Ingest, error) {
	reader := csv.NewReader(io.LimitReader(r, 1<<20))
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 || len(rows) > 101 {
		return nil, errors.New("CSV requires header and 1..100 rows")
	}
	out := []p.Ingest{}
	for _, row := range rows[1:] {
		v := map[string]string{}
		for i, col := range rows[0] {
			v[col] = row[i]
		}
		status := v["fetch_status"]
		if status == "" {
			status = "SUCCESS"
		}
		httpStatus, _ := strconv.Atoi(v["http_status"])
		x := p.Ingest{Company: v["company"], Title: v["title"], JobType: v["job_type"], Locations: strings.Split(v["locations"], "|"), SourceID: v["source_id"], ExternalID: v["external_id"], URL: v["url"], Text: v["text"], FetchStatus: status, HTTPStatus: httpStatus}
		if err = validateImport(x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}
func JSON(r io.Reader) ([]p.Ingest, error) {
	var out []p.Ingest
	decoder := json.NewDecoder(io.LimitReader(r, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	if len(out) == 0 || len(out) > 100 {
		return nil, errors.New("1..100 jobs required")
	}
	for _, i := range out {
		if err := validateImport(i); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func validateImport(i p.Ingest) error {
	if i.Text == "" && i.URL != "" {
		i.Text = "pending public fetch"
		i.FetchStatus = "SUCCESS"
	}
	return i.Validate()
}
