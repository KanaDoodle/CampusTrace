package transport

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

func resumeFailure(w http.ResponseWriter, err error) {
	code := resumeDraftFailure(err)
	status := http.StatusBadGateway
	if code == "MODEL_TIMEOUT" {
		status = http.StatusGatewayTimeout
	}
	requestID := w.Header().Get("X-Request-ID")
	out := map[string]any{"error": code, "code": code, "request_id": requestID}
	fields := []any{"category", code, "request_id", requestID}
	var validation *resume.ValidationError
	if errors.As(err, &validation) {
		out["diagnostic"] = validation.Diagnostic
		fields = append(fields, "diagnostic", validation.Diagnostic)
	}
	// Never log err.Error(): provider errors may contain private text or URLs.
	slog.Warn("resume draft failed", fields...)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(out)
}
