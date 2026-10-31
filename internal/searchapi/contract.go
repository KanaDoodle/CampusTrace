// Package searchapi defines the versioned Core/Search wire contract. It has no
// dependency on Core persistence, authentication, matching, or model execution.
package searchapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const Protocol = "campustrace-search-v1"

// One additional hit lets Core verify ACL before exposing a continuation hint.
const MaxHits = 1001
const MaxBatch = 64

var ErrPending = errors.New("search event not applied yet")
var ErrUnavailable = errors.New("search unavailable")
var ErrConflict = errors.New("search generation or version conflict")
var ErrInvalid = errors.New("invalid search request")
var identifier = regexp.MustCompile(`^[0-9a-f]{32}$`)

func ID(v string) bool { return identifier.MatchString(v) }

type Document struct {
	Dataset    string   `json:"dataset"`
	ID         string   `json:"id"`
	Version    int64    `json:"version"`
	Hash       string   `json:"hash"`
	Visibility string   `json:"visibility"`
	Owner      string   `json:"owner"`
	Company    string   `json:"company"`
	Title      string   `json:"title"`
	Locations  []string `json:"locations"`
	JobType    string   `json:"job_type"`
	Status     string   `json:"status"`
	Text       string   `json:"text"`
	Deleted    bool     `json:"deleted"`
}

func (d Document) ContentHash() string {
	d.Version = 0
	d.Hash = ""
	raw, _ := json.Marshal(d)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (d Document) Valid() bool {
	if !ID(d.Dataset) || !ID(d.ID) || d.Version < 1 || d.Hash != d.ContentHash() || len(d.Text) > 60000 || len(d.Company) > 200 || len(d.Title) > 300 || len(d.Locations) > 300 {
		return false
	}
	for _, location := range d.Locations {
		if len(location) > 256 {
			return false
		}
	}
	if d.Deleted {
		return d.Text == "" && d.Owner == "" && d.Title == "" && d.Company == ""
	}
	if d.Visibility != "GLOBAL" && d.Visibility != "PRIVATE" {
		return false
	}
	if d.Visibility == "GLOBAL" && d.Owner != "" || d.Visibility == "PRIVATE" && !ID(d.Owner) {
		return false
	}
	return d.JobType == "FULL_TIME" || d.JobType == "INTERNSHIP" || d.JobType == "UNKNOWN"
}

type Query struct {
	Dataset    string `json:"dataset"`
	Generation string `json:"generation"`
	User       string `json:"user"`
	Text       string `json:"text"`
	Mode       string `json:"mode"`
	Exclude    string `json:"exclude"`
	Company    string `json:"company"`
	City       string `json:"city"`
	JobType    string `json:"job_type"`
	Limit      int    `json:"limit"`
}

func (q *Query) Normalize() error {
	q.Text = strings.TrimSpace(q.Text)
	q.Exclude = strings.TrimSpace(q.Exclude)
	if q.Mode == "" {
		q.Mode = "ALL"
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Mode != "ALL" && q.Mode != "ANY" || q.Limit < 1 || q.Limit > MaxHits || len(q.Text) > 1000 || len(q.Exclude) > 1000 || len(q.Company) > 200 || len(q.City) > 200 || len(strings.Fields(q.Text)) > 12 || len(strings.Fields(q.Exclude)) > 12 {
		return ErrInvalid
	}
	if q.JobType != "" && q.JobType != "FULL_TIME" && q.JobType != "INTERNSHIP" && q.JobType != "UNKNOWN" {
		return ErrInvalid
	}
	return nil
}

type Hit struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Hash    string `json:"hash"`
}
type Result struct {
	Protocol   string `json:"protocol"`
	Generation string `json:"generation"`
	Hits       []Hit  `json:"hits"`
	More       bool   `json:"more"`
}
type Status struct {
	Rebuild    *Rebuild `json:"rebuild,omitempty"`
	Protocol   string   `json:"protocol"`
	Generation string   `json:"generation"`
}
type Batch struct {
	Protocol   string     `json:"protocol"`
	Generation string     `json:"generation"`
	Documents  []Document `json:"documents"`
}
type Transport interface {
	Status(context.Context) (Status, error)
	Apply(context.Context, Batch) error
	Query(context.Context, Query) (Result, error)
}

var technologies = map[string]*regexp.Regexp{
	"c++": regexp.MustCompile(`(?i)(^|[^a-z0-9_])c\+\+(?:[0-9]{2})?([^a-z0-9_]|$)`),
	"c#":  regexp.MustCompile(`(?i)(^|[^a-z0-9_])c#(?:[0-9]{1,2}(?:\.[0-9]+)?)?([^a-z0-9_]|$)`),
	"go":  regexp.MustCompile(`(?i)(^|[^a-z0-9_])(go|golang)(?:1\.[0-9]+(?:\.[0-9]+)?)?([^a-z0-9_]|$)`),
}

func Technology(term string) string {
	v := strings.ToLower(term)
	if v == "golang" {
		v = "go"
	}
	if technologies[v] != nil {
		return v
	}
	return ""
}
func (d Document) Technologies() []string {
	out := []string{}
	text := d.Title + " " + d.Company + " " + d.Text
	for _, key := range []string{"c++", "c#", "go"} {
		if technologies[key].MatchString(text) {
			out = append(out, key)
		}
	}
	return out
}

// Matches is the one-document convenience form. Reuse PrepareMatcher when
// scanning a catalog so query normalization and scratch storage are amortized.
func (q Query) Matches(d Document) bool { return q.PrepareMatcher().Matches(d) }

// AppliedQuery is an internal visibility fence. It contains no JD text.
type AppliedQuery struct {
	Exact      bool   `json:"exact,omitempty"`
	Protocol   string `json:"protocol"`
	Generation string `json:"generation"`
	Dataset    string `json:"dataset"`
	Hits       []Hit  `json:"hits"`
}
type AppliedResult struct {
	Protocol   string `json:"protocol"`
	Generation string `json:"generation"`
	Ready      bool   `json:"ready"`
}
