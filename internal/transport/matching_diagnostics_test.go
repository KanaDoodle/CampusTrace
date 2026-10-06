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

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestMatchingDiagnosticsExplainStageWithoutLoggingSensitiveErrors(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		err         error
		stage, code string
		provider    int
	}{
		{&analysis.HTTPError{Status: 502}, "EXTRACT", "MODEL_PROVIDER_FAILED", 502},
		{&url.Error{Op: "Post", URL: "https://provider.invalid/?api_key=synthetic-secret", Err: errors.New("private candidate text")}, "EXTRACT", "MODEL_CONNECTION_FAILED", 0},
		{&url.Error{Op: "Post", URL: "https://provider.invalid/", Err: context.DeadlineExceeded}, "EXTRACT", "MODEL_TIMEOUT", 0},
		{&analysis.ResponseError{Reason: "INVALID_JSON"}, "EXTRACT", "MODEL_RESPONSE_INVALID", 0},
		{modelconfig.ErrInvalid, "EXTRACT", "MODEL_ENDPOINT_BLOCKED", 0},
		{matching.ErrInvalid, "COMPARE", "MATCH_OUTPUT_INVALID", 0},
	} {
		w := httptest.NewRecorder()
		w.Header().Set("X-Request-ID", "aabbccddeeff00112233445566778899")
		matchFailure(w, tc.err, tc.stage)
		var out struct {
			Code       string `json:"code"`
			Request    string `json:"request_id"`
			Diagnostic struct {
				Stage  string `json:"stage"`
				Status int    `json:"provider_status"`
			} `json:"diagnostic"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if w.Code != 502 || out.Code != tc.code || out.Diagnostic.Stage != tc.stage || out.Diagnostic.Status != tc.provider || out.Request == "" {
			t.Fatal(w.Body.String())
		}
		if strings.Contains(w.Body.String(), "synthetic-secret") || strings.Contains(w.Body.String(), "private candidate") {
			t.Fatal("sensitive provider error returned")
		}
	}
	if strings.Contains(logs.String(), "synthetic-secret") || strings.Contains(logs.String(), "private candidate") {
		t.Fatal("sensitive error logged")
	}
	if !strings.Contains(logs.String(), `"stage":"EXTRACT"`) {
		t.Fatal("missing failure stage")
	}
}

func TestMatchingValidationDiagnosticKeepsOnlyReasonAndNumericPositions(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	err := &matching.ValidationError{Reason: "MATCH_COUNT", JobIndex: 1, ItemIndex: 2, Expected: 15, Actual: 9}
	w := httptest.NewRecorder()
	matchFailure(w, err, "COMPARE")
	var out struct {
		Code       string `json:"code"`
		Diagnostic struct {
			Reason   string `json:"validation_reason"`
			Job      int    `json:"job_index"`
			Item     int    `json:"item_index"`
			Expected int    `json:"expected"`
			Actual   int    `json:"actual"`
		} `json:"diagnostic"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || w.Code != 502 || out.Code != "MATCH_OUTPUT_INVALID" || out.Diagnostic.Reason != "MATCH_COUNT" || out.Diagnostic.Job != 1 || out.Diagnostic.Item != 2 || out.Diagnostic.Expected != 15 || out.Diagnostic.Actual != 9 {
		t.Fatal(w.Body.String())
	}
	if !strings.Contains(logs.String(), "MATCH_COUNT") {
		t.Fatal("missing safe validation reason")
	}
}

func TestUnavailableMatchingJobPreservesCompatibilityError(t *testing.T) {
	w := httptest.NewRecorder()
	matchFailure(w, p.ErrMatchJobUnavailable)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || out["code"] != "MATCH_JOB_UNAVAILABLE" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMatchingCapacityDiagnosticsExplainProfileVersusComparison(t *testing.T) {
	for _, reason := range []string{"CANDIDATE_BYTES", "CANDIDATE_FACTS", "COMPARISON_BYTES"} {
		err := &matching.CapacityError{Reason: reason, Actual: 33000, Limit: 32000}
		w := httptest.NewRecorder()
		matchFailure(w, err)
		var out struct {
			Code       string         `json:"code"`
			Diagnostic map[string]any `json:"diagnostic"`
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || w.Code != 400 || out.Code != "MATCH_CAPACITY" || out.Diagnostic["capacity_reason"] != reason || out.Diagnostic["actual"] != float64(33000) || out.Diagnostic["limit"] != float64(32000) {
			t.Fatal("capacity origin was hidden", w.Body.String())
		}
		code, task := matchTaskFailure(err, "COMPARE")
		if code != "MATCH_CAPACITY" || task["capacity_reason"] != reason || task["actual"] != 33000 || task["limit"] != 32000 {
			t.Fatal("background task lost input capacity diagnostics", task)
		}
	}
}
