// Package searchservice owns only the disposable search index. It never opens
// Core's MySQL, reads candidate profiles, or decides business authorization.
package searchservice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/observability"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

type Backend struct {
	URL, Alias string
	HTTP       *http.Client
}

func NewBackend(endpoint, alias string) (*Backend, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || !regexp.MustCompile(`^[a-z][a-z0-9-]{2,60}$`).MatchString(alias) {
		return nil, a.ErrInvalid
	}
	return &Backend{strings.TrimRight(endpoint, "/"), alias, &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (b *Backend) request(ctx context.Context, method, path string, body []byte) (status int, raw []byte, err error) {
	ctx, span := observability.Client(ctx, "search.index")
	defer func() { observability.End(span, err) }()
	req, e := http.NewRequestWithContext(ctx, method, b.URL+path, bytes.NewReader(body))
	if e != nil {
		return 0, nil, a.ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := b.HTTP.Do(req)
	if e != nil {
		return 0, nil, a.ErrUnavailable
	}
	defer resp.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if e != nil || len(raw) > 8<<20 {
		return resp.StatusCode, nil, a.ErrUnavailable
	}
	return resp.StatusCode, raw, nil
}
func mapping(alias string) []byte {
	keyword := map[string]string{"type": "keyword"}
	text := map[string]string{"type": "text", "analyzer": "cjk"}
	fields := map[string]any{"dataset": keyword, "id": keyword, "version": map[string]string{"type": "long"}, "hash": keyword, "visibility": keyword, "owner": keyword, "company": text, "company_exact": keyword, "title": text, "locations": keyword, "job_type": keyword, "status": keyword, "text": text, "deleted": map[string]string{"type": "boolean"}, "tech_tokens": keyword}
	v := map[string]any{"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0, "refresh_interval": "500ms"}, "mappings": map[string]any{"dynamic": "strict", "properties": fields}}
	if alias != "" {
		v["aliases"] = map[string]any{alias: map[string]any{}}
	}
	raw, _ := json.Marshal(v)
	return raw
}
func (b *Backend) current(ctx context.Context) (index, generation string, err error) {
	status, raw, e := b.request(ctx, "GET", "/_alias/"+b.Alias, nil)
	if e != nil {
		return "", "", e
	}
	if status == 404 {
		status, _, e = b.request(ctx, "PUT", "/"+b.Alias+"-initial", mapping(b.Alias))
		if e != nil || status != 200 && status != 400 {
			return "", "", a.ErrUnavailable
		}
		if status == 400 {
			if _, e = b.Rebuild(ctx); e != nil {
				return "", "", e
			}
		}
		status, raw, e = b.request(ctx, "GET", "/_alias/"+b.Alias, nil)
	}
	if e != nil || status != 200 {
		return "", "", a.ErrUnavailable
	}
	var indices map[string]json.RawMessage
	if json.Unmarshal(raw, &indices) != nil || len(indices) != 1 {
		return "", "", a.ErrConflict
	}
	for name := range indices {
		index = name
	}
	if !strings.HasPrefix(index, b.Alias+"-") {
		return "", "", a.ErrConflict
	}
	status, raw, e = b.request(ctx, "GET", "/"+index+"/_settings?filter_path=*.settings.index.uuid", nil)
	var settings map[string]struct {
		Settings struct {
			Index struct {
				UUID string `json:"uuid"`
			} `json:"index"`
		} `json:"settings"`
	}
	if e != nil || status != 200 || json.Unmarshal(raw, &settings) != nil || settings[index].Settings.Index.UUID == "" {
		return "", "", a.ErrUnavailable
	}
	return index, index + ":" + settings[index].Settings.Index.UUID, nil
}
func (b *Backend) Status(ctx context.Context) (a.Status, error) {
	_, generation, e := b.current(ctx)
	return a.Status{Protocol: a.Protocol, Generation: generation}, e
}
func (b *Backend) Apply(ctx context.Context, batch a.Batch) error {
	if batch.Protocol != a.Protocol || len(batch.Documents) < 1 || len(batch.Documents) > a.MaxBatch {
		return a.ErrInvalid
	}
	for _, doc := range batch.Documents {
		if !doc.Valid() {
			return a.ErrInvalid
		}
	}
	index, generation, e := b.current(ctx)
	if e != nil {
		return e
	}
	if generation != batch.Generation {
		return a.ErrConflict
	}
	var body bytes.Buffer
	for _, doc := range batch.Documents {
		meta := map[string]any{"index": map[string]any{"_index": index, "_id": doc.Dataset + "-" + doc.ID, "version": doc.Version, "version_type": "external"}}
		line, _ := json.Marshal(meta)
		body.Write(line)
		body.WriteByte('\n')
		var fields map[string]any
		raw, _ := json.Marshal(doc)
		_ = json.Unmarshal(raw, &fields)
		fields["tech_tokens"] = doc.Technologies()
		fields["company_exact"] = strings.ToLower(doc.Company)
		line, _ = json.Marshal(fields)
		body.Write(line)
		body.WriteByte('\n')
	}
	status, raw, e := b.request(ctx, "POST", "/_bulk?refresh=wait_for", body.Bytes())
	if e != nil || status != 200 {
		return a.ErrUnavailable
	}
	var result struct {
		Items []struct {
			Index struct {
				Status int `json:"status"`
			} `json:"index"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Items) != len(batch.Documents) {
		return a.ErrUnavailable
	}
	for i, item := range result.Items {
		if item.Index.Status == 200 || item.Index.Status == 201 {
			continue
		}
		if item.Index.Status != 409 {
			return a.ErrUnavailable
		}
		incoming := batch.Documents[i]
		status, raw, e = b.request(ctx, "GET", "/"+index+"/_doc/"+incoming.Dataset+"-"+incoming.ID, nil)
		var saved struct {
			Source a.Document `json:"_source"`
		}
		if e != nil || status != 200 || json.Unmarshal(raw, &saved) != nil {
			return a.ErrUnavailable
		}
		if saved.Source.Version < incoming.Version || saved.Source.Version == incoming.Version && saved.Source.Hash != incoming.Hash {
			return a.ErrConflict
		}
	}
	return nil
}
func term(field string, value any) any { return map[string]any{"term": map[string]any{field: value}} }
func (b *Backend) Query(ctx context.Context, q a.Query) (a.Result, error) {
	out := a.Result{Protocol: a.Protocol, Hits: []a.Hit{}}
	if q.Normalize() != nil || !a.ID(q.User) || !a.ID(q.Dataset) {
		return out, a.ErrInvalid
	}
	index, generation, e := b.current(ctx)
	if e != nil {
		return out, e
	}
	if generation != q.Generation {
		return out, a.ErrConflict
	}
	out.Generation = generation
	acl := map[string]any{"bool": map[string]any{"should": []any{term("visibility", "GLOBAL"), map[string]any{"bool": map[string]any{"filter": []any{term("visibility", "PRIVATE"), term("owner", q.User)}}}}, "minimum_should_match": 1}}
	filters := []any{term("dataset", q.Dataset), term("deleted", false), acl}
	if q.Company != "" {
		filters = append(filters, term("company_exact", strings.ToLower(q.Company)))
	}
	if q.City != "" {
		filters = append(filters, term("locations", q.City))
	}
	if q.JobType != "" {
		filters = append(filters, term("job_type", q.JobType))
	}
	clause := func(word string) any {
		if tech := a.Technology(word); tech != "" {
			return term("tech_tokens", tech)
		}
		return map[string]any{"multi_match": map[string]any{"query": word, "fields": []string{"title^4", "company^2", "text", "locations"}, "operator": "and"}}
	}
	words := []any{}
	for _, word := range strings.Fields(q.Text) {
		words = append(words, clause(word))
	}
	boolQuery := map[string]any{"filter": filters}
	if len(words) > 0 {
		if q.Mode == "ANY" {
			boolQuery["should"] = words
			boolQuery["minimum_should_match"] = 1
		} else {
			boolQuery["must"] = words
		}
	}
	excludes := []any{}
	for _, word := range strings.Fields(q.Exclude) {
		excludes = append(excludes, clause(word))
	}
	if len(excludes) > 0 {
		boolQuery["must_not"] = excludes
	}
	input := map[string]any{"size": q.Limit + 1, "track_total_hits": false, "_source": []string{"id", "version", "hash"}, "query": map[string]any{"bool": boolQuery}, "sort": []any{map[string]string{"_score": "desc"}, map[string]string{"id": "asc"}}}
	body, _ := json.Marshal(input)
	status, raw, e := b.request(ctx, "POST", "/"+index+"/_search", body)
	if e != nil || status != 200 {
		return out, a.ErrUnavailable
	}
	var result struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				Source a.Hit `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if json.Unmarshal(raw, &result) != nil || result.TimedOut || result.Shards.Failed > 0 {
		return out, a.ErrUnavailable
	}
	for i, hit := range result.Hits.Hits {
		if i >= q.Limit {
			out.More = true
			break
		}
		if !a.ID(hit.Source.ID) || hit.Source.Version < 1 || len(hit.Source.Hash) != 64 {
			return out, a.ErrUnavailable
		}
		out.Hits = append(out.Hits, hit.Source)
	}
	return out, nil
}
func (b *Backend) Rebuild(ctx context.Context) (a.Status, error) {
	bytes := make([]byte, 8)
	if _, e := rand.Read(bytes); e != nil {
		return a.Status{}, a.ErrUnavailable
	}
	index := b.Alias + "-" + hex.EncodeToString(bytes)
	status, _, e := b.request(ctx, "PUT", "/"+index, mapping(""))
	if e != nil || status != 200 {
		return a.Status{}, a.ErrUnavailable
	}
	body, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"remove": map[string]any{"index": "*", "alias": b.Alias, "must_exist": false}}, map[string]any{"add": map[string]any{"index": index, "alias": b.Alias}}}})
	status, _, e = b.request(ctx, "POST", "/_aliases", body)
	if e != nil || status != 200 {
		return a.Status{}, a.ErrUnavailable
	}
	return b.Status(ctx)
}
