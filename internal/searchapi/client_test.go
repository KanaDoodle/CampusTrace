package searchapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEscapedBatchFitsRequestBoundWithoutLosingDocuments(t *testing.T) {
	var calls, documents atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 4<<20 {
			t.Error("batch exceeds bounded request size")
		}
		var b Batch
		if json.Unmarshal(raw, &b) != nil {
			t.Error("invalid batch")
		}
		documents.Add(int64(len(b.Documents)))
		calls.Add(1)
		json.NewEncoder(w).Encode(Status{Protocol: Protocol, Generation: b.Generation})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, strings.Repeat("q", 32), strings.Repeat("w", 32))
	if err != nil {
		t.Fatal(err)
	}
	docs := make([]Document, 64)
	for i := range docs {
		docs[i] = Document{Text: strings.Repeat("\x01", 60000)}
	}
	if err = client.Apply(context.Background(), Batch{Protocol: Protocol, Generation: "generation", Documents: docs}); err != nil {
		t.Fatal(err)
	}
	if documents.Load() != 64 || calls.Load() < 2 {
		t.Fatal("batch not split safely", documents.Load(), calls.Load())
	}
}
