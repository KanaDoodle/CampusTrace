// retrieval-eval reads only the explicit dataset. Live paid calls require
// -live, an explicit public endpoint and key environment variable.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	file := flag.String("dataset", "internal/rag/testdata/retrieval.json", "explicit, sanitized labeled dataset")
	output := flag.String("output", "", "optional JSON output path")
	live := flag.Bool("live", false, "explicitly allow paid embedding/rerank requests")
	embeddingURL := flag.String("embedding-url", "", "public HTTPS embeddings endpoint")
	embeddingModel := flag.String("embedding-model", "", "embedding model")
	keyEnv := flag.String("key-env", "RETRIEVAL_API_KEY", "name of API key environment variable")
	rerankURL := flag.String("rerank-url", "", "optional public HTTPS rerank endpoint")
	rerankModel := flag.String("rerank-model", "", "optional rerank model")
	rerankKeyEnv := flag.String("rerank-key-env", "RETRIEVAL_API_KEY", "name of reranker API key environment variable")
	maxCalls := flag.Int("max-calls", 12, "refuse before first call if worst-case budget is insufficient")
	flag.Parse()
	if *maxCalls < 1 || *maxCalls > 192 {
		return errors.New("max-calls must be 1..192")
	}
	f, e := os.Open(*file)
	if e != nil {
		return errors.New("cannot read explicit dataset")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return errors.New("dataset exceeds 1 MiB")
	}
	var dataset rag.Dataset
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&dataset); e != nil {
		return errors.New("invalid dataset JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("extra dataset data")
	}
	if e = dataset.Validate(); e != nil {
		return e
	}
	configured := *embeddingURL != "" || *embeddingModel != "" || *rerankURL != "" || *rerankModel != ""
	if configured && !*live {
		return errors.New("model configuration requires explicit -live")
	}
	if *live && *embeddingURL == "" {
		return errors.New("live evaluation requires an embedding endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	chunks := dataset.Chunks()
	service := rag.Service{Cache: &rag.QueryCache{}}
	vectors := map[string]rag.SemanticVector{}
	baseline, e := rag.Evaluate(ctx, dataset, "keyword", func(ctx context.Context, q string) (rag.SearchResult, error) {
		return service.Rank(ctx, "evaluation", q, chunks, nil, 20)
	})
	if e != nil {
		return e
	}
	reports := []rag.EvalReport{baseline}
	indexCalls := 0
	if *live {
		embedding := modelconfig.Config{URL: *embeddingURL, Model: *embeddingModel, APIKey: os.Getenv(*keyEnv)}
		if e = embedding.Validate(); e != nil {
			return errors.New("invalid embedding configuration")
		}
		service.Options.Embedding = &embedding
		var reranker *modelconfig.Config
		if *rerankURL != "" || *rerankModel != "" {
			cfg := modelconfig.Config{URL: *rerankURL, Model: *rerankModel, APIKey: os.Getenv(*rerankKeyEnv)}
			if e = cfg.Validate(); e != nil {
				return errors.New("invalid rerank configuration")
			}
			reranker = &cfg
		}
		planned := (len(chunks)+31)/32 + len(dataset.Queries)
		if reranker != nil {
			planned += len(dataset.Queries)
		}
		if planned > *maxCalls {
			return errors.New("planned provider calls exceed max-calls; no requests sent")
		}
		calls := 0
		service.Provider.Before = func(context.Context) error {
			calls++
			if calls > *maxCalls {
				return errors.New("provider call budget reached")
			}
			return nil
		}
		for start := 0; start < len(chunks); start += 32 {
			end := min(start+32, len(chunks))
			texts := []string{}
			for _, c := range chunks[start:end] {
				texts = append(texts, rag.Redact(c.Title+"\n"+c.Text, ""))
			}
			vs, _, err := service.Provider.Embed(ctx, embedding, texts)
			if err != nil {
				return fmt.Errorf("index failed: %s", err)
			}
			indexCalls++
			for i, c := range chunks[start:end] {
				vectors[c.ID] = rag.NewSemanticVector(c, vs[i], "")
			}
		}
		cached := map[string]rag.SearchResult{}
		report, err := rag.Evaluate(ctx, dataset, "hybrid", func(ctx context.Context, q string) (rag.SearchResult, error) {
			out, e := service.Rank(ctx, "evaluation", q, chunks, vectors, 20)
			cached[q] = out
			return out, e
		})
		if err != nil {
			return err
		}
		reports = append(reports, report)
		if reranker != nil {
			reranked, err := rag.Evaluate(ctx, dataset, "hybrid+rerank", func(ctx context.Context, q string) (rag.SearchResult, error) {
				start := time.Now()
				out := cached[q]
				out.Retrieval.EmbeddingCalls = 0
				out.Retrieval.QueryCacheHit = false
				out.Retrieval.Usage = rag.Usage{}
				out.Retrieval.Warnings = append([]string{}, out.Retrieval.Warnings...)
				if len(out.Hits) > 1 {
					texts := []string{}
					for _, h := range out.Hits {
						texts = append(texts, rag.Redact(h.Title+"\n"+h.Text, ""))
					}
					out.Retrieval.RerankCalls = 1
					scores, e := service.Provider.Rerank(ctx, *reranker, rag.Redact(q, ""), texts)
					if e != nil {
						out.Retrieval.Warnings = append(out.Retrieval.Warnings, "RETRIEVAL_PROVIDER_FAILED")
					} else {
						out.Hits = rag.ApplyRerank(out.Hits, scores)
						out.Retrieval.Mode += "+rerank"
					}
				}
				out.Retrieval.Milliseconds = time.Since(start).Milliseconds()
				return out, nil
			})
			if err != nil {
				return err
			}
			reports = append(reports, reranked)
		}

	}
	result := map[string]any{"reports": reports, "index_provider_calls": indexCalls, "note": "synthetic cases verify workflow only; human-labeled results depend on this dataset, corpus and selected model; latency excludes index creation"}
	raw, e = json.MarshalIndent(result, "", "  ")
	if e != nil {
		return e
	}
	if *output != "" {
		if e = os.WriteFile(*output, append(raw, '\n'), 0600); e != nil {
			return errors.New("cannot write evaluation report")
		}
	}
	fmt.Println(string(raw))
	return nil
}
