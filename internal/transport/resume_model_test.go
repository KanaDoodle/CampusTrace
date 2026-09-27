package transport

import (
	"context"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"testing"
	"time"
)

func TestResumeModelTimeoutAndFailureCodes(t *testing.T) {
	client := analysis.NewChat("https://api.deepseek.com/chat/completions", "test-key", "deepseek-flash", 1)
	model := resumeModelWithTimeout(client).(*analysis.ChatClient)
	if client.HTTP.Timeout != 25*time.Second || model.HTTP.Timeout != 90*time.Second || model.Sem != client.Sem {
		t.Fatal("resume timeout did not preserve shared model state")
	}
	for _, item := range []struct {
		err  error
		code string
	}{
		{context.DeadlineExceeded, "MODEL_TIMEOUT"},
		{&analysis.HTTPError{Status: 401}, "MODEL_AUTH_FAILED"},
		{&analysis.HTTPError{Status: 402}, "MODEL_BALANCE_LOW"},
		{&analysis.HTTPError{Status: 429}, "MODEL_PROVIDER_BUSY"},
		{&analysis.HTTPError{Status: 422}, "MODEL_REQUEST_INVALID"},
		{resume.ErrInvalid, "RESUME_DRAFT_UNVERIFIABLE"},
		{errors.New("network"), "MODEL_PROVIDER_FAILED"},
	} {
		if got := resumeDraftFailure(item.err); got != item.code {
			t.Fatalf("%v: %s", item.err, got)
		}
	}
}
