package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

const MaxIndexBatch = 32
const MaxDimensions = 4096
const RetrievalVersion = "bm25-rrf60-v1"
const SemanticVersion = "redacted-title-chunk-v1"

// Credentials belong to this authenticated request, never to model arguments,
// database records, cache keys, traces, or response diagnostics.
type Options struct {
	Embedding *modelconfig.Config `json:"embedding,omitempty"`
	Rerank    *modelconfig.Config `json:"rerank,omitempty"`
	MaskName  string              `json:"mask_name,omitempty"`
}

func (o Options) Validate() error {
	if len(o.MaskName) > 200 {
		return modelconfig.ErrInvalid
	}
	for _, c := range []*modelconfig.Config{o.Embedding, o.Rerank} {
		if c != nil {
			if err := c.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}
func modelKey(c *modelconfig.Config) string {
	if c == nil {
		return ""
	}
	return d.Hash(c.URL + "\n" + c.Model + "\n" + SemanticVersion)
}
func (o Options) Identity() string {
	return d.Hash(RetrievalVersion + modelKey(o.Embedding) + modelKey(o.Rerank) + o.MaskName)
}
func Redact(text, mask string) string {
	if mask != "" {
		text = strings.ReplaceAll(text, mask, "[已遮盖姓名]")
	}
	return resume.Redact(text)
}
func inputText(c Chunk, mask string) string { return Redact(c.Title+"\n"+c.Text, mask) }

type ProviderError struct{ Code string }

func (e *ProviderError) Error() string { return e.Code }
func providerError(code string) error  { return &ProviderError{Code: code} }

type Usage struct {
	InputTokens *int `json:"input_tokens,omitempty"`
}
type Provider struct {
	HTTP   *http.Client
	Before func(context.Context) error
}

func (p Provider) post(ctx context.Context, cfg modelconfig.Config, payload any, out any) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if p.Before != nil {
		if err := p.Before(ctx); err != nil {
			return err
		}
	}
	timeout := 6 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline)-250*time.Millisecond)
	}
	if timeout <= 0 {
		return providerError("RETRIEVAL_TIMEOUT")
	}
	ctx, done := context.WithTimeout(ctx, timeout)
	defer done()
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 128000 {
		return providerError("RETRIEVAL_INPUT_LIMIT")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(raw))
	if err != nil {
		return modelconfig.ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := p.HTTP
	if client == nil {
		client = modelconfig.PublicClient()
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return providerError("RETRIEVAL_TIMEOUT")
		}
		return providerError("RETRIEVAL_CONNECTION_FAILED")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		switch response.StatusCode {
		case 401, 403:
			return providerError("RETRIEVAL_AUTH_FAILED")
		case 402:
			return providerError("RETRIEVAL_BALANCE_LOW")
		case 429:
			return providerError("RETRIEVAL_RATE_LIMIT")
		default:
			return providerError("RETRIEVAL_PROVIDER_FAILED")
		}
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || json.Unmarshal(raw, out) != nil {
		return providerError("RETRIEVAL_RESPONSE_INVALID")
	}
	return nil
}
func validVector(v []float64) bool {
	if len(v) < 1 || len(v) > MaxDimensions {
		return false
	}
	norm := 0.0
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 1e6 {
			return false
		}
		norm += x * x
	}
	return norm > 0
}
func (p Provider) Embed(ctx context.Context, cfg modelconfig.Config, texts []string) ([][]float64, Usage, error) {
	var usage Usage
	if len(texts) < 1 || len(texts) > MaxIndexBatch {
		return nil, usage, providerError("RETRIEVAL_INPUT_LIMIT")
	}
	for _, text := range texts {
		if strings.TrimSpace(text) == "" || len(text) > 10000 {
			return nil, usage, providerError("RETRIEVAL_INPUT_LIMIT")
		}
	}
	var out struct {
		Data []struct {
			Index  *int      `json:"index"`
			Vector []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			Tokens *int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	err := p.post(ctx, cfg, map[string]any{"model": cfg.Model, "input": texts, "encoding_format": "float"}, &out)
	if err != nil {
		return nil, usage, err
	}
	if len(out.Data) != len(texts) {
		return nil, usage, providerError("RETRIEVAL_RESPONSE_INVALID")
	}
	vectors := make([][]float64, len(texts))
	dimension := len(out.Data[0].Vector)
	for _, v := range out.Data {
		if v.Index == nil || *v.Index < 0 || *v.Index >= len(texts) || vectors[*v.Index] != nil || len(v.Vector) != dimension || !validVector(v.Vector) {
			return nil, usage, providerError("RETRIEVAL_RESPONSE_INVALID")
		}
		vectors[*v.Index] = v.Vector
	}
	if out.Usage.Tokens != nil && *out.Usage.Tokens >= 0 {
		usage.InputTokens = out.Usage.Tokens
	}
	return vectors, usage, nil
}

type RerankScore struct {
	Index int
	Score float64
}

func (p Provider) Rerank(ctx context.Context, cfg modelconfig.Config, query string, texts []string) ([]RerankScore, error) {
	if len(texts) < 1 || len(texts) > 20 || len(query) > 1000 {
		return nil, providerError("RETRIEVAL_INPUT_LIMIT")
	}
	var out struct {
		Results []struct {
			Index *int     `json:"index"`
			Score *float64 `json:"relevance_score"`
		} `json:"results"`
	}
	err := p.post(ctx, cfg, map[string]any{"model": cfg.Model, "query": query, "documents": texts, "top_n": len(texts), "return_documents": false}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Results) != len(texts) {
		return nil, providerError("RETRIEVAL_RESPONSE_INVALID")
	}
	seen := map[int]bool{}
	scores := []RerankScore{}
	for _, v := range out.Results {
		if v.Index == nil || v.Score == nil || *v.Index < 0 || *v.Index >= len(texts) || seen[*v.Index] || math.IsNaN(*v.Score) || math.IsInf(*v.Score, 0) || *v.Score < 0 || *v.Score > 1 {
			return nil, providerError("RETRIEVAL_RESPONSE_INVALID")
		}
		seen[*v.Index] = true
		scores = append(scores, RerankScore{*v.Index, *v.Score})
	}
	return scores, nil
}
func failureCode(err error) string {
	var v *ProviderError
	if errors.As(err, &v) {
		return v.Code
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "RETRIEVAL_TIMEOUT"
	}
	return "RETRIEVAL_UNAVAILABLE"
}
