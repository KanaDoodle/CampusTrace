//go:build !kanarpc_stream

package inferencebridge

import (
	"strings"
	"testing"
)

func TestPublishedSDKBuildRejectsStreamConfiguration(t *testing.T) {
	client, err := New(Config{Address: "127.0.0.1:1", Model: "synthetic-model", ScopeKey: []byte("synthetic-scope-secret-long-enough")})
	if client != nil || err == nil || !strings.Contains(err.Error(), "kanarpc_stream") {
		t.Fatalf("default build must reject unpublished streaming SDK usage: %v %v", client, err)
	}
}
