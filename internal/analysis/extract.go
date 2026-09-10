package analysis

import (
	"context"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
	"strings"
)

// Parser extracts explicit labelled facts plus conservative common JD signals.
// Ambiguous language remains missing evidence, never an inferred business status.
func Extract(ctx context.Context, text string) ([]d.Claim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(text) > 60000 {
		return nil, errors.New("schema: JD too large")
	}
	out := []d.Claim{}
	seen := map[string]bool{}
	add := func(t, v, x string) {
		key := t + v
		if !seen[key] {
			out = append(out, d.Claim{Type: t, Value: v, Excerpt: x, Method: "RULE", Confidence: 1})
			seen[key] = true
		}
	}
	labels := map[string]string{"graduation": "GRADUATION_REQUIREMENT", "degree": "EDUCATION_REQUIREMENT", "job_type": "JOB_TYPE", "location": "LOCATION", "experience_months": "EXPERIENCE_REQUIREMENT", "tech": "TECH_STACK", "language": "LANGUAGE_REQUIREMENT", "major": "MAJOR_REQUIREMENT", "apply": "APPLY_ACTION", "deadline": "DEADLINE", "closed": "CLOSED_SIGNAL"}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		kv := strings.SplitN(line, ":", 2)
		if len(kv) == 2 {
			if typ := labels[strings.ToLower(kv[0])]; typ != "" {
				if typ != "APPLY_ACTION" || !d.NegativeApplication(line) {
					add(typ, strings.TrimSpace(kv[1]), line)
				}
			}
		}
	}
	for _, line := range strings.Split(text, "\n") {
		low := strings.ToLower(line)
		if strings.Contains(line, "本科及以上") {
			add("EDUCATION_REQUIREMENT", "BACHELOR", line)
		}
		if strings.Contains(line, "硕士及以上") {
			add("EDUCATION_REQUIREMENT", "MASTER", line)
		}
		if m := regexp.MustCompile(`(20\d{2})届`).FindStringSubmatch(line); len(m) > 1 {
			add("GRADUATION_REQUIREMENT", m[1], line)
		}
		if d.NegativeApplication(line) {
			add("CLOSED_SIGNAL", "CLOSED", line)
		}
		if d.PositiveApplication(line) && !strings.HasPrefix(strings.TrimSpace(low), "apply:") {
			add("APPLY_ACTION", "PRESENT", line)
		}
		if !strings.HasPrefix(low, "tech:") && (regexp.MustCompile(`(?i)\b(go|golang)\b`).MatchString(line)) {
			v := "go"
			if strings.Contains(line, "任一种") || strings.Contains(low, " or ") {
				v = "go|java|c++"
			}
			add("TECH_STACK", v, line)
		}
	}
	for _, c := range out {
		if err := c.Validate(text); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func DecodeClaims(raw []byte, text string) ([]d.Claim, error) {
	var c d.Claims
	if err := d.Strict(raw, &c); err != nil {
		return nil, err
	}
	var rawClaims struct {
		Claims []map[string]json.RawMessage `json:"claims"`
	}
	if err := json.Unmarshal(raw, &rawClaims); err != nil {
		return nil, err
	}
	for _, v := range rawClaims.Claims {
		if _, ok := v["confidence"]; !ok {
			return nil, errors.New("schema: confidence required")
		}
	}
	if c.Claims == nil || len(c.Claims) > 64 {
		return nil, errors.New("invalid claims list")
	}
	for _, v := range c.Claims {
		if err := v.Validate(text); err != nil {
			return nil, err
		}
	}
	return c.Claims, nil
}
