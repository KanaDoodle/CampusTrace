package web

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

// Keep the downloadable format example executable against the actual importer.
// A schema example that our own program rejects would mislead the chat model.
func TestChatPromptExamplePassesStrictSchemaAndMatchingValidation(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	raw, err := exec.Command("node", "-e", "process.stdout.write(JSON.stringify(require('./matching_chat.js').resultExample))").Output()
	if err != nil {
		t.Fatal(err)
	}
	var doc matching.ChatDocument
	if err = d.StrictLimit(raw, &doc, 2<<20); err != nil {
		t.Fatal("export example violates import schema", err)
	}
	if doc.PromptRevision != matching.ChatPromptRevision || doc.Version != matching.ChatVersion {
		t.Fatal("prompt and API revision differ")
	}
	c := matching.Candidate{Facts: []matching.Fact{{ID: "示例语言事实编号", Kind: "LANGUAGE", Text: "Go"}}}
	_, err = matching.ImportChatJob(doc.Jobs[0], "熟悉 Go。本科及以上学历。有以下方向至少一种经验：后端开发、检索优化", d.Job{ID: "example"}, d.Profile{}, c, time.Time{})
	if err != nil {
		var all *matching.ChatValidationErrors
		if errors.As(err, &all) {
			t.Fatal("export example rejected by matching validation", all.Issues)
		}
		t.Fatal("export example rejected by matching validation", err)
	}
}
