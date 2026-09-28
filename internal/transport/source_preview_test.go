package transport

import (
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/source"
)

func TestSourcePreviewFailureSeparatesNetworkFromWebsiteChanges(t *testing.T) {
	for category, want := range map[string]string{
		"TIMEOUT_OR_NETWORK": "SOURCE_PREVIEW_NETWORK",
		"BLOCKED":            "SOURCE_PREVIEW_BLOCKED",
		"HTTP_TRANSIENT":     "SOURCE_PREVIEW_BUSY",
		"SCHEMA_INVALID":     "SOURCE_PREVIEW_CHANGED",
	} {
		if got := sourcePreviewFailure(&source.FetchError{Category: category}); got != want {
			t.Fatalf("%s: got %s, want %s", category, got, want)
		}
	}
}
