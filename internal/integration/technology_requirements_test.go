package integration

import (
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
)

func TestTechnologyAnalysisDoesNotReuseLegacyClaimsOrReactivateOldGeneration(t *testing.T) {
	ctx, s, q, owner, source := kafkaSetup(t)
	input := ingest(t, ctx, s, source)
	input.Text = "graduation: 2027\ndegree: BACHELOR\njob_type: FULL_TIME\napply: PRESENT\n必须掌握Go或Rust任一种语言"
	observation, err := s.Ingest(ctx, input)
	must(t, err)
	// Exact pre-upgrade namespace; using ProcessingVersion with an old config
	// name now deliberately produces a NEW semantic namespace instead.
	legacyVersion := "processing-v3-" + d.Hash(d.JSON([]string{"claims-v3-semantics", d.ParserVersion, d.SourceParserVersion, observation.ParserVersion}))[:48]
	legacy, err := s.BindAnalysis(ctx, p.NewTask("ANALYZE", observation.ID), legacyVersion)
	must(t, err)
	bad := []d.Claim{{Type: "TECH_STACK", Value: "go|java|c++", Excerpt: "必须掌握Go或Rust任一种语言", Method: "RULE", Confidence: 1}}
	must(t, s.PersistAnalysis(ctx, observation.ID, legacyVersion, bad))
	legacyKey := q.Prefix + "analysis:cache-v2:" + legacyVersion + ":" + d.Hash(input.Text)
	must(t, q.R.Set(ctx, legacyKey, d.JSON(bad), time.Hour).Err())
	worker := worker(s, q)
	worker.Version = "claims-v1" // Existing deployment default must still upgrade.
	counter := &countAnalysis{}
	worker.Analyzer = counter
	task := p.NewTask("ANALYZE", observation.ID)
	must(t, worker.Process(ctx, task))
	current, err := s.Observation(ctx, observation.ID)
	must(t, err)
	want := d.ProcessingVersion("claims-v1", d.ParserVersion, observation.ParserVersion)
	if want == legacyVersion || current.AnalysisVersion != want || current.ActiveAnalysisGeneration <= legacy.Generation || counter.calls.Load() != 1 {
		t.Fatal("legacy receipt/cache suppressed new extraction", current, counter.calls.Load())
	}
	must(t, worker.Process(ctx, task))
	if counter.calls.Load() != 1 {
		t.Fatal("idempotent receipt reran extraction")
	}
	// Replayed late legacy results remain history; they cannot become active.
	must(t, s.PersistAnalysis(ctx, observation.ID, legacyVersion, bad))
	current, err = s.Observation(ctx, observation.ID)
	must(t, err)
	evidence, err := s.Evidence(ctx, observation.JobID)
	must(t, err)
	_, active := rules.Current([]d.Observation{current}, evidence)
	found := false
	for _, item := range active {
		if item.AnalysisVersion != want || item.Value == "go|java|c++" {
			t.Fatal("legacy claims became current", item)
		}
		found = found || item.Type == "TECH_STACK" && item.Value == "REQUIRED:go|rust"
	}
	if !found || len(evidence) <= len(active) {
		t.Fatal("new claim missing or history removed", evidence, active)
	}
	must(t, s.SaveProfile(ctx, owner, d.Profile{Degree: "BACHELOR", GraduationYear: 2027, PreferredTypes: []string{"FULL_TIME"}, Skills: []string{"Rust"}}))
	evaluation, err := s.ComputeEvaluation(ctx, owner, observation.JobID)
	must(t, err)
	if evaluation.Eligibility.Status != "ELIGIBLE" {
		t.Fatal("persisted Rust alternative was not consumed", evaluation.Eligibility)
	}
}

func TestTechnologyPersistedEvidenceFeedsCurrentQualification(t *testing.T) {
	ctx, s, q, _, source := kafkaSetup(t)
	for _, tc := range []struct {
		text   string
		skills []string
		want   string
	}{
		{"必须掌握Go和C++", []string{"Go", "cpp"}, "PASS"},
		{"必须掌握Go和C++", []string{"Go"}, "FAIL"},
		{"必须掌握Go或Rust任一种语言", []string{"Java"}, "FAIL"},
		{"任职要求：熟悉Go；C++经验优先", []string{"Golang"}, "PASS"},
		{"任职要求\n符合以下任意两条：\n熟悉C/C++", []string{"Go"}, "UNKNOWN"},
	} {
		t.Run(tc.text+"/"+d.JSON(tc.skills), func(t *testing.T) {
			input := ingest(t, ctx, s, source)
			input.Text = tc.text
			observation, err := s.Ingest(ctx, input)
			must(t, err)
			must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", observation.ID)))
			current, err := s.Observation(ctx, observation.ID)
			must(t, err)
			evidence, err := s.Evidence(ctx, observation.JobID)
			must(t, err)
			_, active := rules.Current([]d.Observation{current}, evidence)
			assessment := rules.Eligibility(d.Job{}, d.Profile{Skills: tc.skills}, active, time.Now())
			for _, result := range assessment.Results {
				if result.Rule == "TECH_STACK" {
					if result.Result != tc.want || len(result.EvidenceIDs) == 0 {
						t.Fatal("persisted qualification differs", result, tc.want)
					}
					return
				}
			}
			t.Fatal("missing persisted technology rule")
		})
	}
}
