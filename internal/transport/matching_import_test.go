package transport

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatDecoderHasItsOwnBoundAndRetainsStrictSchema(t *testing.T) {
	raw := `{"document":{"version":"campustrace-chat-v3","candidate_hash":"hash","jobs":[]},"mask_name":""}`
	large := strings.Repeat(" ", 70000) + raw
	var in chatImportRequest
	if err := decodeChatImport(httptest.NewRequest("POST", "/", strings.NewReader(large)), &in); err != nil {
		t.Fatal(err)
	}
	if err := d.Strict([]byte(large), &in); err == nil {
		t.Fatal("ordinary strict limit was weakened")
	}
	for _, bad := range []string{strings.Replace(raw, `"jobs":[]`, `"jobs":null`, 1), strings.Replace(raw, `"mask_name":""`, `"mask_name":"","api_key":"synthetic"`, 1), raw + "{}"} {
		if err := decodeChatImport(httptest.NewRequest("POST", "/", strings.NewReader(bad)), &in); err == nil {
			t.Fatal("accepted invalid wire schema")
		}
	}
	if err := decodeChatImport(httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat(" ", maxChatImportBytes+1))), &in); err != matching.ErrCapacity {
		t.Fatal(err)
	}
}
