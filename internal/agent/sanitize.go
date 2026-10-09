package agent

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

// Redact string values, not the serialized JSON: a numeric value resembling a
// phone number must never be replaced with a string inside invalid JSON.
func safeObservation(value any, mask string) (any, error) {
	b, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if e = dec.Decode(&v); e != nil {
		return nil, e
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if mask != "" {
				x = strings.ReplaceAll(x, mask, "[已遮盖姓名]")
			}
			return resume.Redact(x)
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = walk(x[k])
			}
		}
		return v
	}
	return walk(v), nil
}
