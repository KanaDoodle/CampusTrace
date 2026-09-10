package domain

import (
	"testing"
)

func TestStrict(t *testing.T) {
	for _, raw := range []string{`null`, `{"x":null}`, `{"x":1,"z":2}`, `{"x":1} {}`, `{"x":1} trailing`, `[]`} {
		var v struct {
			X int `json:"x"`
		}
		if Strict([]byte(raw), &v) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	var v struct {
		X int `json:"x"`
	}
	if err := Strict([]byte(`{"x":1}`), &v); err != nil {
		t.Fatal(err)
	}
}
func TestBinding(t *testing.T) {
	c := Claim{Type: "TECH_STACK", Value: "go", Excerpt: "Go backend", Method: "LLM", Confidence: .9}
	if err := c.Validate("Seeking Go backend engineers"); err != nil {
		t.Fatal(err)
	}
	c.Excerpt = "Java only"
	if c.Validate("Seeking Go backend engineers") == nil {
		t.Fatal("fabricated excerpt")
	}
	c.Excerpt = "Go backend"
	c.Confidence = 1.1
	if c.Validate("Go backend") == nil {
		t.Fatal("range")
	}
}
func TestTransitions(t *testing.T) {
	for _, v := range [][2]string{{"PLANNED", "APPLIED"}, {"APPLIED", "OA"}, {"OA", "INTERVIEW"}, {"INTERVIEW", "INTERVIEW"}, {"HR", "OFFER"}} {
		if err := Transition(v[0], v[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range [][2]string{{"PLANNED", "OFFER"}, {"REJECTED", "APPLIED"}, {"OFFER", "OA"}, {"PLANNED", "CLOSED"}} {
		if Transition(v[0], v[1]) == nil {
			t.Fatal("invalid accepted", v)
		}
	}
}

func TestNormalizedClaimEnums(t *testing.T) {
	for _, c := range []Claim{{Type: "JOB_TYPE", Value: "ANYTHING"}, {Type: "EDUCATION_REQUIREMENT", Value: "wizard"}, {Type: "DEADLINE", Value: "yesterday"}, {Type: "EXPERIENCE_REQUIREMENT", Value: "-1"}, {Type: "GRADUATION_REQUIREMENT", Value: "2028-2026"}} {
		c.Excerpt = "text"
		c.Method = "LLM"
		c.Confidence = 1
		if c.Validate("text") == nil {
			t.Fatal("invalid normalized requirement", c)
		}
	}
}
