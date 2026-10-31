package searchservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

// Control state lives beside the derived index, not in a Search process. CAS
// serializes operator transitions across replicas; SQL owns the data barrier.
type rebuildRecord struct {
	Plan        a.Rebuild `json:"plan"`
	TargetIndex string    `json:"target_index"`
	Rollback    string    `json:"rollback_generation"`
	Seq         int64     `json:"-"`
	Term        int64     `json:"-"`
}

func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, e := rand.Read(raw); e != nil {
		return "", a.ErrUnavailable
	}
	return hex.EncodeToString(raw), nil
}
func (b *Backend) rebuildRecord(ctx context.Context) (*rebuildRecord, error) {
	code, raw, e := b.request(ctx, "GET", "/"+b.Alias+"-control/_doc/rebuild", nil)
	if e != nil {
		return nil, e
	}
	if code == 404 {
		return nil, nil
	}
	if code != 200 {
		return nil, a.ErrUnavailable
	}
	var saved struct {
		Source rebuildRecord `json:"_source"`
		Seq    int64         `json:"_seq_no"`
		Term   int64         `json:"_primary_term"`
	}
	if json.Unmarshal(raw, &saved) != nil || !a.ID(saved.Source.Plan.Operation) {
		return nil, a.ErrConflict
	}
	saved.Source.Seq, saved.Source.Term = saved.Seq, saved.Term
	return &saved.Source, nil
}
func (b *Backend) saveRebuild(ctx context.Context, r *rebuildRecord, create bool) error {
	raw, _ := json.Marshal(r)
	path := "/" + b.Alias + "-control/_doc/rebuild"
	if create {
		path += "?op_type=create"
	} else {
		path += fmt.Sprintf("?if_seq_no=%d&if_primary_term=%d", r.Seq, r.Term)
	}
	code, _, e := b.request(ctx, "PUT", path, raw)
	if e != nil {
		return e
	}
	if code == 409 {
		return a.ErrConflict
	}
	if code != 200 && code != 201 {
		return a.ErrUnavailable
	}
	return nil
}
func (b *Backend) physicalGeneration(ctx context.Context, index string) (string, error) {
	if !strings.HasPrefix(index, b.Alias+"-") || !indexName.MatchString(index) {
		return "", a.ErrInvalid
	}
	code, raw, e := b.request(ctx, "GET", "/"+index+"/_settings?filter_path=*.settings.index.uuid", nil)
	var data map[string]struct {
		Settings struct {
			Index struct {
				UUID string `json:"uuid"`
			} `json:"index"`
		} `json:"settings"`
	}
	if e != nil || code != 200 || json.Unmarshal(raw, &data) != nil || data[index].Settings.Index.UUID == "" {
		return "", a.ErrUnavailable
	}
	return index + ":" + data[index].Settings.Index.UUID, nil
}
func (b *Backend) resolveGeneration(ctx context.Context, generation string, write bool) (string, error) {
	active, current, e := b.current(ctx)
	if e != nil {
		return "", e
	}
	if generation == current {
		return active, nil
	}
	r, e := b.rebuildRecord(ctx)
	if e != nil {
		return "", e
	}
	if r == nil {
		return "", a.ErrConflict
	}
	p := r.Plan
	allowed := generation == p.Source && (!write || p.Phase == "BUILDING" || p.Phase == "CUTTING" || p.Phase == "COMMITTED")
	allowed = allowed || generation == p.Target && (p.Phase == "BUILDING" || p.Phase == "CUTTING" || p.Phase == "COMMITTED")
	if !allowed {
		return "", a.ErrConflict
	}
	index, _, ok := strings.Cut(generation, ":")
	if !ok {
		return "", a.ErrInvalid
	}
	actual, e := b.physicalGeneration(ctx, index)
	if e != nil {
		return "", e
	}
	if actual != generation {
		return "", a.ErrConflict
	}
	return index, nil
}
func (b *Backend) Rebuild(ctx context.Context) (a.Status, error)  { return b.beginRebuild(ctx, false) }
func (b *Backend) Rollback(ctx context.Context) (a.Status, error) { return b.beginRebuild(ctx, true) }
func (b *Backend) beginRebuild(ctx context.Context, rollback bool) (a.Status, error) {
	_, source, e := b.current(ctx)
	if e != nil {
		return a.Status{}, e
	}
	old, e := b.rebuildRecord(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if old != nil && (old.Plan.Phase == "BUILDING" || old.Plan.Phase == "CUTTING" || old.Plan.Phase == "COMMITTED") {
		if rollback && old.Plan.Mode != "rollback" {
			return a.Status{}, a.ErrConflict
		}
		return b.prepareCandidate(ctx, old)
	}
	id, e := randomID()
	if e != nil {
		return a.Status{}, e
	}
	r := &rebuildRecord{Plan: a.Rebuild{Operation: id, Phase: "BUILDING", Mode: "rebuild", Source: source}, TargetIndex: b.Alias + "-" + id[:16]}
	if old != nil {
		r.Seq, r.Term = old.Seq, old.Term
		r.Rollback = old.Rollback
	}
	if rollback {
		if r.Rollback == "" {
			return a.Status{}, a.ErrConflict
		}
		index, _, ok := strings.Cut(r.Rollback, ":")
		if !ok {
			return a.Status{}, a.ErrConflict
		}
		actual, e := b.physicalGeneration(ctx, index)
		if e != nil || actual != r.Rollback {
			return a.Status{}, a.ErrConflict
		}
		r.TargetIndex = index
		r.Plan.Target = actual
		r.Plan.Mode = "rollback"
	}
	// Mapping is deliberately dynamic:false: metadata is read by document ID only.
	control, _ := json.Marshal(map[string]any{"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0}, "mappings": map[string]any{"dynamic": false}})
	code, _, e := b.request(ctx, "PUT", "/"+b.Alias+"-control", control)
	if e != nil || code != 200 && code != 400 {
		return a.Status{}, a.ErrUnavailable
	}
	if e = b.saveRebuild(ctx, r, old == nil); e != nil {
		return a.Status{}, e
	}
	saved, e := b.rebuildRecord(ctx)
	if e != nil {
		return a.Status{}, e
	}
	return b.prepareCandidate(ctx, saved)
}
func (b *Backend) prepareCandidate(ctx context.Context, r *rebuildRecord) (a.Status, error) {
	if r.Plan.Phase != "BUILDING" || r.Plan.Target != "" {
		return b.Status(ctx)
	}
	code, _, e := b.request(ctx, "PUT", "/"+r.TargetIndex, mapping(""))
	if e != nil || code != 200 && code != 400 {
		return a.Status{}, a.ErrUnavailable
	}
	r.Plan.Target, e = b.physicalGeneration(ctx, r.TargetIndex)
	if e != nil {
		return a.Status{}, e
	}
	if e = b.saveRebuild(ctx, r, false); e != nil {
		return a.Status{}, e
	}
	return b.Status(ctx)
}
func (b *Backend) ClaimRebuild(ctx context.Context, in a.RebuildRef) (a.Status, error) {
	if !a.ID(in.Operation) || !a.ID(in.Dataset) {
		return a.Status{}, a.ErrInvalid
	}
	r, e := b.rebuildRecord(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if r == nil || r.Plan.Operation != in.Operation || r.Plan.Target == "" || r.Plan.Phase != "BUILDING" && r.Plan.Phase != "CUTTING" && r.Plan.Phase != "COMMITTED" {
		return a.Status{}, a.ErrConflict
	}
	if r.Plan.Dataset != "" && r.Plan.Dataset != in.Dataset {
		return a.Status{}, a.ErrConflict
	}
	if r.Plan.Dataset == "" {
		r.Plan.Dataset = in.Dataset
		if e = b.saveRebuild(ctx, r, false); e != nil {
			return a.Status{}, e
		}
	}
	return b.Status(ctx)
}
func (b *Backend) CommitRebuild(ctx context.Context, in a.RebuildCommit) (a.Status, error) {
	if !a.ID(in.Operation) || !a.ID(in.Dataset) || in.Version < 0 || in.Documents < 0 {
		return a.Status{}, a.ErrInvalid
	}
	r, e := b.rebuildRecord(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if r == nil || r.Plan.Operation != in.Operation || r.Plan.Dataset != in.Dataset || r.Plan.Target == "" || r.Plan.Phase == "ABORTED" || r.Plan.Phase == "DONE" {
		return a.Status{}, a.ErrConflict
	}
	if in.Version < r.Plan.Version {
		return a.Status{}, a.ErrConflict
	}
	target, e := b.physicalGeneration(ctx, r.TargetIndex)
	if e != nil || target != r.Plan.Target {
		return a.Status{}, a.ErrConflict
	}
	_, active, e := b.current(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if active != r.Plan.Source && active != r.Plan.Target {
		return a.Status{}, a.ErrConflict
	}
	body, _ := json.Marshal(map[string]any{"query": term("dataset", in.Dataset)})
	code, raw, e := b.request(ctx, "POST", "/"+r.TargetIndex+"/_count", body)
	var count struct {
		Count  int64 `json:"count"`
		Shards struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
	}
	if e != nil || code != 200 || json.Unmarshal(raw, &count) != nil || count.Shards.Failed != 0 {
		return a.Status{}, a.ErrUnavailable
	}
	if count.Count != in.Documents {
		return a.Status{}, a.ErrConflict
	}
	r.Plan.Phase = "CUTTING"
	r.Plan.Version = in.Version
	r.Plan.Documents = in.Documents
	if e = b.saveRebuild(ctx, r, false); e != nil {
		return a.Status{}, e
	}
	if active == r.Plan.Source {
		source, _, ok := strings.Cut(r.Plan.Source, ":")
		if !ok {
			return a.Status{}, a.ErrConflict
		}
		body, _ = json.Marshal(map[string]any{"actions": []any{map[string]any{"remove": map[string]any{"index": source, "alias": b.Alias, "must_exist": true}}, map[string]any{"add": map[string]any{"index": r.TargetIndex, "alias": b.Alias}}}})
		code, _, e = b.request(ctx, "POST", "/_aliases", body)
		if e != nil || code != 200 {
			return a.Status{}, a.ErrUnavailable
		}
	}
	// A lost alias reply leaves CUTTING durable. Retry inspects the real alias;
	// it never guesses that a failed HTTP call implies the swap did not happen.
	latest, e := b.rebuildRecord(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if latest.Plan.Operation != in.Operation {
		return a.Status{}, a.ErrConflict
	}
	if latest.Plan.Phase == "DONE" {
		return b.Status(ctx)
	}
	if latest.Plan.Phase != "CUTTING" && latest.Plan.Phase != "COMMITTED" {
		return a.Status{}, a.ErrConflict
	}
	latest.Plan.Phase = "COMMITTED"
	latest.Rollback = latest.Plan.Source
	if e = b.saveRebuild(ctx, latest, false); e != nil {
		return a.Status{}, e
	}
	return b.Status(ctx)
}
func (b *Backend) AckRebuild(ctx context.Context, in a.RebuildRef) (a.Status, error) {
	r, e := b.rebuildRecord(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if r == nil || r.Plan.Operation != in.Operation || r.Plan.Dataset != in.Dataset || r.Plan.Phase != "COMMITTED" && r.Plan.Phase != "DONE" {
		return a.Status{}, a.ErrConflict
	}
	_, active, e := b.current(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if active != r.Plan.Target {
		return a.Status{}, a.ErrConflict
	}
	if r.Plan.Phase != "DONE" {
		r.Plan.Phase = "DONE"
		if e = b.saveRebuild(ctx, r, false); e != nil {
			return a.Status{}, e
		}
	}
	return b.Status(ctx)
}
func (b *Backend) AbortRebuild(ctx context.Context, in a.RebuildRef) (a.Status, error) {
	if !a.ID(in.Operation) {
		return a.Status{}, a.ErrInvalid
	}
	r, e := b.rebuildRecord(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if r == nil || r.Plan.Operation != in.Operation {
		return a.Status{}, a.ErrConflict
	}
	if r.Plan.Phase == "ABORTED" {
		return b.Status(ctx)
	}
	if r.Plan.Phase != "BUILDING" {
		return a.Status{}, a.ErrConflict
	}
	_, active, e := b.current(ctx)
	if e != nil {
		return a.Status{}, e
	}
	if active == r.Plan.Target {
		return a.Status{}, a.ErrConflict
	}
	r.Plan.Phase = "ABORTED"
	if e = b.saveRebuild(ctx, r, false); e != nil {
		return a.Status{}, e
	}
	return b.Status(ctx)
}

// indexName excludes URL metacharacters even when a token is supplied by a
// trusted internal client; UUID resolution still rejects a recreated index.
var indexName = regexp.MustCompile(`^[a-z][a-z0-9-]{2,120}$`)
