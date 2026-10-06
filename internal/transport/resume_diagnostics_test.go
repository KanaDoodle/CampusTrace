package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

func TestResumeDiagnosticsRetainPositionsWithoutLoggingPrivateErrors(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	err := &resume.ValidationError{Diagnostic: resume.Diagnostic{Reason: "EXCERPT_NOT_EXACT", Scope: "FACT", ProjectIndex: 2, ItemIndex: 3}}
	w := httptest.NewRecorder()
	w.Header().Set("X-Request-ID", "aabbccddeeff00112233445566778899")
	resumeFailure(w, err)
	var body struct {
		Code       string            `json:"code"`
		Diagnostic resume.Diagnostic `json:"diagnostic"`
		Request    string            `json:"request_id"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != 502 || body.Code != "RESUME_DRAFT_UNVERIFIABLE" || body.Diagnostic != err.Diagnostic || body.Request == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Body.String())
	}
	for _, failure := range []error{&url.Error{Op: "Post", URL: "https://provider.invalid/?api_key=synthetic-secret", Err: errors.New("private resume text")}, context.DeadlineExceeded} {
		out := httptest.NewRecorder()
		resumeFailure(out, failure)
		if strings.Contains(out.Body.String(), "synthetic-secret") || strings.Contains(out.Body.String(), "private resume") {
			t.Fatal("private error returned")
		}
		if errors.Is(failure, context.DeadlineExceeded) && out.Code != 504 {
			t.Fatal(out.Code)
		}
	}
	if strings.Contains(logs.String(), "synthetic-secret") || strings.Contains(logs.String(), "private resume") {
		t.Fatal("private error logged")
	}
	if !strings.Contains(logs.String(), "EXCERPT_NOT_EXACT") {
		t.Fatal("missing safe diagnostic")
	}
}
