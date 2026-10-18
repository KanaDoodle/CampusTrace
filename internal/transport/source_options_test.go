package transport

import (
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"testing"
)

func TestSourceOptionsUseSchedulingPolicyForEveryCampusAdapter(t *testing.T) {
	for _, site := range source.CampusSites() {
		src := d.Source{ID: "source", Adapter: site.Adapter, Tenant: "campus", Name: site.Company}
		options := sourceOptions([]d.Source{src})
		if len(options) != 1 || options[0].Source != src || options[0].MinimumInterval != site.MinimumInterval {
			t.Fatalf("%s: %+v", site.Adapter, options)
		}
		data, err := json.Marshal(options[0])
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err = json.Unmarshal(data, &body); err != nil || body["minimum_interval"] != float64(site.MinimumInterval) || body["id"] != src.ID {
			t.Fatalf("source metadata must stay at top level: %s (%v)", data, err)
		}
	}
	if got := sourceOptions([]d.Source{{Adapter: "custom"}})[0].MinimumInterval; got != 300 {
		t.Fatalf("non-campus interval: %d", got)
	}
	if out := sourceOptions(nil); out == nil || len(out) != 0 {
		t.Fatalf("empty source list must encode as an array: %+v", out)
	}
}
