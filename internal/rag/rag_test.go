package rag

import (
	"math"
	"testing"
)

func TestKeywordRetrievalAndSemanticCosine(t *testing.T) {
	if Cosine([]float64{0}, []float64{0}) != 0 {
		t.Fatal("zero")
	}
	if math.Abs(Cosine([]float64{1, 0}, []float64{1, 0})-1) > 1e-9 {
		t.Fatal("cosine")
	}
	a := Chunk{ID: "redis", Title: "Redis Streams", Text: "PEL ACK XAUTOCLAIM pending recovery"}
	b := Chunk{ID: "sql", Title: "SQL joins", Text: "nested loop merge join"}
	hits := KeywordRank("Redis pending", []Chunk{b, a})
	if len(hits) == 0 || hits[0].ID != a.ID {
		t.Fatal("irrelevant retrieval")
	}
	if len(Tokens("面试复盘")) != 3 {
		t.Fatal("Chinese bigrams")
	}
}
