package rag

import (
	"math"
	"testing"
)

func TestHybrid(t *testing.T) {
	if Cosine([]float64{0}, []float64{0}) != 0 {
		t.Fatal("zero")
	}
	if math.Abs(Cosine([]float64{1, 0}, []float64{1, 0})-1) > 1e-9 {
		t.Fatal("cosine")
	}
	a := Chunk{ID: "redis", Title: "Redis Streams", Text: "PEL ACK XAUTOCLAIM pending recovery"}
	a.Vector = Embed(a.Title + " " + a.Text)
	b := Chunk{ID: "sql", Title: "SQL joins", Text: "nested loop merge join"}
	b.Vector = Embed(b.Title + " " + b.Text)
	hits := KeywordRank("Redis pending", []Chunk{b, a})
	if len(hits) == 0 || hits[0].ID != a.ID {
		t.Fatal("irrelevant retrieval")
	}
	if len(Tokens("面试复盘")) != 3 {
		t.Fatal("Chinese bigrams")
	}
}
