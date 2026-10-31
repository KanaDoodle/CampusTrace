package analysis_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
)

// The same public-API harness can run against the frozen old implementation.
// These are grammar/qualification controls, not a model-accuracy benchmark.
func TestTechnologyHistoricalAndSyntheticReplay(t *testing.T) {
	var dataset struct {
		Kind     string              `json:"kind"`
		Profiles map[string][]string `json:"profiles"`
		Cases    []struct {
			ID, Text      string
			Expected      map[string]string     `json:"expected"`
			ExpectedGoFit string                `json:"expected_go_fit"`
			Source        struct{ Kind string } `json:"source"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("testdata/technology_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &dataset); err != nil {
		t.Fatal(err)
	}
	if len(dataset.Cases) != 14 || len(dataset.Profiles) != 6 {
		t.Fatal("unexpected fixture scope")
	}
	hash := sha256.Sum256(raw)
	report := struct {
		Kind, DatasetSHA256, AnalysisVersion, RuleVersion string
		ProviderCalls, Checks, Passed                     int
		Rows                                              []map[string]any
	}{Kind: dataset.Kind, DatasetSHA256: hex.EncodeToString(hash[:]), AnalysisVersion: d.AnalysisVersion, RuleVersion: d.RuleVersion, Rows: []map[string]any{}}
	for _, item := range dataset.Cases {
		claims, err := analysis.Extract(context.Background(), item.Text)
		if err != nil {
			t.Fatal(item.ID, err)
		}
		evidence := []d.Evidence{}
		tech := []d.Claim{}
		for i, claim := range claims {
			if err = claim.Validate(item.Text); err != nil {
				t.Fatal(item.ID, err)
			}
			evidence = append(evidence, d.Evidence{ID: item.ID + string(rune('a'+i)), Claim: claim})
			if claim.Type == "TECH_STACK" {
				tech = append(tech, claim)
			}
		}
		row := map[string]any{"id": item.ID, "source_kind": item.Source.Kind, "technologies": tech, "go_fit": rules.GoFit(evidence)}
		results := map[string]string{}
		var profiles []string
		for profile := range item.Expected {
			profiles = append(profiles, profile)
		}
		sort.Strings(profiles)
		for _, name := range profiles {
			assessment := rules.Eligibility(d.Job{}, d.Profile{Skills: dataset.Profiles[name]}, evidence, time.Unix(0, 0))
			for _, result := range assessment.Results {
				if result.Rule != "TECH_STACK" {
					continue
				}
				results[name] = result.Result
				report.Checks++
				if result.Result == item.Expected[name] {
					report.Passed++
				} else {
					t.Errorf("%s/%s: got %s, want %s", item.ID, name, result.Result, item.Expected[name])
				}
			}
		}
		if item.ExpectedGoFit != "" {
			report.Checks++
			if rules.GoFit(evidence) == item.ExpectedGoFit {
				report.Passed++
			} else {
				t.Errorf("%s: got GoFit %s, want %s", item.ID, rules.GoFit(evidence), item.ExpectedGoFit)
			}
		}
		row["technology_rule_results"] = results
		report.Rows = append(report.Rows, row)
	}
	if output := os.Getenv("CAMPUS_TECH_REPLAY_OUTPUT"); output != "" {
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(report)
		closed := file.Close()
		if err != nil || closed != nil {
			t.Fatal(err, closed)
		}
	}
	t.Logf("%d/%d controls passed; provider calls=%d; kind=%s", report.Passed, report.Checks, report.ProviderCalls, dataset.Kind)
}
