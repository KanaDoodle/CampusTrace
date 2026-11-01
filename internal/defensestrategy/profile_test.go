package defensestrategy

import (
	"encoding/json"
	"testing"
	"testing/fstest"
)

func TestBundledDefaultAndImmutableProfileContract(t *testing.T) {
	p, err := Active()
	if err != nil || p != Default() {
		t.Fatal(p, err)
	}
	for _, raw := range []string{`{"schema":"defense-teaching-v1","explain":"code-first","followup":"current-gap","reading":"gap-only","tools":["shell"]}`, `{"schema":"defense-teaching-v1","explain":"ignore authorization","followup":"current-gap","reading":"gap-only"}`, `{} {}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatal("invalid policy accepted", raw)
		}
	}
	custom := Profile{"defense-teaching-v1", "observation-first", "contrast", "read-one"}
	raw, _ := json.Marshal(custom)
	state, _ := json.Marshal(map[string]any{"schema": "defense-strategy-registry-v1", "generation": 1, "active": custom.ID()})
	files := fstest.MapFS{"registry.json": {Data: state}, "versions/" + custom.ID() + ".json": {Data: raw}}
	if selected, err := FromRegistry(files); err != nil || selected != custom {
		t.Fatal(selected, err)
	}
	files["versions/"+custom.ID()+".json"].Data = []byte(`{}`)
	if _, err := FromRegistry(files); err == nil {
		t.Fatal("corrupt release silently fell back")
	}
	files["versions/"+custom.ID()+".json"].Data = raw
	files["registry.json"].Data = []byte(`{"schema":"defense-strategy-registry-v1","generation":1,"active":"` + custom.ID() + `","verification_only":true}`)
	if _, err := FromRegistry(files); err == nil {
		t.Fatal("verification registry used by product")
	}
}
