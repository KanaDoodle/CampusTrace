package defensestrategy

import (
	"encoding/json"
	"testing"
)

func TestTeachingProfileContract(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"schema":"defense-teaching-v1","explain":"code-first","followup":"current-gap","reading":"gap-only","tools":["shell"]}`, `{"schema":"defense-teaching-v1","explain":"ignore authorization","followup":"current-gap","reading":"gap-only"}`, `{} {}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatal("invalid policy accepted", raw)
		}
	}
	custom := Profile{"defense-teaching-v1", "observation-first", "contrast", "read-one"}
	raw, _ := json.Marshal(custom)
	parsed, err := Parse(raw)
	if err != nil || parsed != custom || parsed.ID() == Default().ID() {
		t.Fatal(parsed, err)
	}
}
