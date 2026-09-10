package analysis

import (
	"context"
	"testing"
)

func TestExtractAndSchema(t *testing.T) {
	claims, err := Extract(context.Background(), "graduation: 2027\ndegree: BACHELOR\ntech: REQUIRED:go\napply: PRESENT")
	if err != nil || len(claims) != 4 {
		t.Fatal(claims, err)
	}
	for _, raw := range []string{`{"claims":[],"status":"OPEN"}`, `{"claims":null}`, `{"claims":[{"type":"STATUS","value":"OPEN","excerpt":"text","extraction_method":"LLM","confidence":1}]}`, `{"claims":[{"type":"TECH_STACK","value":"go","excerpt":"invented","extraction_method":"LLM","confidence":1}]}`} {
		if _, err := DecodeClaims([]byte(raw), "text"); err == nil {
			t.Fatal("invalid schema accepted", raw)
		}
	}
}

func TestConfidenceRequired(t *testing.T) {
	if _, err := DecodeClaims([]byte(`{"claims":[{"type":"TECH_STACK","value":"go","excerpt":"go","extraction_method":"LLM"}]}`), "go"); err == nil {
		t.Fatal("confidence omitted")
	}
}
