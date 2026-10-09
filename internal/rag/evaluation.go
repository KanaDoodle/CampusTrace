package rag

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"
)

type EvalDocument struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}
type EvalQuery struct {
	ID       string         `json:"id"`
	Query    string         `json:"query"`
	Relevant map[string]int `json:"relevant"`
	Reviewed bool           `json:"reviewed"`
}
type Dataset struct {
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Documents []EvalDocument `json:"documents"`
	Queries   []EvalQuery    `json:"queries"`
}

func (v Dataset) Validate() error {
	if v.Name == "" || len(v.Name) > 200 || (v.Kind != "synthetic" && v.Kind != "human-labeled" && v.Kind != "assistant-reviewed") || len(v.Documents) == 0 || len(v.Documents) > 64 || len(v.Queries) == 0 || len(v.Queries) > 64 {
		return errors.New("invalid retrieval dataset")
	}
	ids := map[string]bool{}
	bytes := 0
	for _, doc := range v.Documents {
		if doc.ID == "" || len(doc.ID) > 128 || ids[doc.ID] || doc.Title == "" || len(doc.Title) > 200 || doc.Text == "" || len(doc.Text) > 60000 {
			return errors.New("invalid evaluation document")
		}
		ids[doc.ID] = true
		bytes += len(doc.Text)
	}
	if bytes > 512000 || len(v.Chunks()) > 256 {
		return errors.New("retrieval dataset capacity")
	}
	queries := map[string]bool{}
	for _, q := range v.Queries {
		if q.ID == "" || queries[q.ID] || len(q.Query) == 0 || len(q.Query) > 1000 || len(q.Relevant) == 0 {
			return errors.New("invalid evaluation query")
		}
		queries[q.ID] = true
		for id, grade := range q.Relevant {
			if !ids[id] || grade < 1 || grade > 3 {
				return errors.New("invalid relevance label")
			}
		}
	}
	return nil
}
func (v Dataset) Chunks() []Chunk {
	chunks := []Chunk{}
	for _, doc := range v.Documents {
		rs := []rune(doc.Text)
		for start, index := 0, 0; start < len(rs); start, index = start+700, index+1 {
			end := min(start+900, len(rs))
			chunks = append(chunks, Chunk{ID: doc.ID + ":" + strconv.Itoa(index), DocumentID: doc.ID, Title: doc.Title, Text: string(rs[start:end]), EmbeddingVersion: EmbeddingVersion, Index: index})
		}
	}
	return chunks
}

type EvalRow struct {
	QueryID     string        `json:"query_id"`
	Reviewed    bool          `json:"reviewed"`
	Recall20    *float64      `json:"recall_at_20,omitempty"`
	NDCG5       *float64      `json:"ndcg_at_5,omitempty"`
	MRR         *float64      `json:"mrr,omitempty"`
	DocumentIDs []string      `json:"document_ids"`
	Retrieval   RetrievalInfo `json:"retrieval"`
	Error       string        `json:"error,omitempty"`
}
type EvalReport struct {
	Dataset       string    `json:"dataset"`
	Kind          string    `json:"kind"`
	Mode          string    `json:"mode"`
	Rows          []EvalRow `json:"rows"`
	Labeled       int       `json:"labeled"`
	Failed        int       `json:"failed"`
	Fallbacks     int       `json:"fallbacks"`
	Recall20      *float64  `json:"recall_at_20,omitempty"`
	NDCG5         *float64  `json:"ndcg_at_5,omitempty"`
	MRR           *float64  `json:"mrr,omitempty"`
	P50MS         int64     `json:"p50_ms"`
	P95MS         int64     `json:"p95_ms"`
	ProviderCalls int       `json:"provider_calls"`
}
type EvalSearch func(context.Context, string) (SearchResult, error)

func retrievalMetrics(ids []string, labels map[string]int) (recall, ndcg, mrr float64) {
	found := map[string]bool{}
	dcg := 0.0
	for i, id := range ids {
		if found[id] {
			continue
		}
		found[id] = true
		if grade := labels[id]; grade > 0 {
			recall++
			if mrr == 0 {
				mrr = 1 / float64(i+1)
			}
			if i < 5 {
				dcg += (math.Pow(2, float64(grade)) - 1) / math.Log2(float64(i+2))
			}
		}
	}
	recall /= float64(len(labels))
	grades := []int{}
	for _, g := range labels {
		grades = append(grades, g)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	ideal := 0.0
	for i, g := range grades {
		if i >= 5 {
			break
		}
		ideal += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i+2))
	}
	if ideal > 0 {
		ndcg = dcg / ideal
	}
	return
}
func Evaluate(ctx context.Context, v Dataset, mode string, search EvalSearch) (EvalReport, error) {
	report := EvalReport{Dataset: v.Name, Kind: v.Kind, Mode: mode, Rows: []EvalRow{}}
	if e := v.Validate(); e != nil {
		return report, e
	}
	durations := []int64{}
	recall, ndcg, mrr := 0.0, 0.0, 0.0
	for _, q := range v.Queries {
		if e := ctx.Err(); e != nil {
			return report, e
		}
		start := time.Now()
		result, e := search(ctx, q.Query)
		durations = append(durations, time.Since(start).Milliseconds())
		row := EvalRow{QueryID: q.ID, Reviewed: q.Reviewed, DocumentIDs: []string{}, Retrieval: result.Retrieval}
		if e != nil {
			row.Error = failureCode(e)
			report.Failed++
		} else {
			seen := map[string]bool{}
			for _, h := range result.Hits {
				if !seen[h.DocumentID] {
					row.DocumentIDs = append(row.DocumentIDs, h.DocumentID)
					seen[h.DocumentID] = true
				}
				if len(row.DocumentIDs) >= 20 {
					break
				}
			}
		}
		if len(result.Retrieval.Warnings) > 0 {
			report.Fallbacks++
		}
		report.ProviderCalls += result.Retrieval.EmbeddingCalls + result.Retrieval.RerankCalls
		// Failed searches stay in the denominator. Unreviewed labels never produce
		// quality scores. Dataset kind preserves who produced the labels;
		// reviewed alone does not imply independent human annotation.
		if q.Reviewed {
			report.Labeled++
			a, b, c := retrievalMetrics(row.DocumentIDs, q.Relevant)
			row.Recall20 = &a
			row.NDCG5 = &b
			row.MRR = &c
			recall += a
			ndcg += b
			mrr += c
		}
		report.Rows = append(report.Rows, row)
	}
	if report.Labeled > 0 {
		n := float64(report.Labeled)
		recall /= n
		ndcg /= n
		mrr /= n
		report.Recall20 = &recall
		report.NDCG5 = &ndcg
		report.MRR = &mrr
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	report.P50MS = durations[(len(durations)-1)/2]
	report.P95MS = durations[int(math.Ceil(.95*float64(len(durations))))-1]
	return report, nil
}
