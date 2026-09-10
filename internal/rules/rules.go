package rules

import (
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Current(observations []d.Observation, evidence []d.Evidence) ([]d.Observation, []d.Evidence) {
	latest := map[string]d.Observation{}
	for _, o := range observations {
		p, ok := latest[o.PostingID]
		if !ok || o.ObservedAt.After(p.ObservedAt) || (o.ObservedAt.Equal(p.ObservedAt) && o.ID > p.ID) {
			latest[o.PostingID] = o
		}
	}
	os := []d.Observation{}
	ids := map[string]bool{}
	versions := map[string]string{}
	for _, o := range latest {
		os = append(os, o)
		if o.FetchStatus == "SUCCESS" {
			ids[o.ID] = true
			versions[o.ID] = o.AnalysisVersion
			if o.ActiveAnalysisGeneration != 0 && (o.CurrentAnalysisGeneration != o.ActiveAnalysisGeneration || o.AnalysisVersion != o.DesiredProcessingVersion) {
				ids[o.ID] = false
			}
		}
	}
	sort.Slice(os, func(i, j int) bool { return os[i].ID < os[j].ID })
	es := []d.Evidence{}
	for _, e := range evidence {
		if ids[e.ObservationID] && (versions[e.ObservationID] == "" || versions[e.ObservationID] == e.AnalysisVersion) {
			es = append(es, e)
		}
	}
	return os, es
}
func Status(job string, observations []d.Observation, evidence []d.Evidence, now time.Time) d.Assessment {
	a := d.Assessment{ID: d.ID(), JobID: job, Status: "UNKNOWN", RuleVersion: d.RuleVersion, AssessedAt: now, EvidenceIDs: []string{}, Reason: "No recent usable observation"}
	os, es := Current(observations, evidence)
	official := false
	failed := false
	open := false
	closed := false
	usable := false
	for _, o := range os {
		if o.Trust != "OFFICIAL" {
			continue
		}
		official = true
		if o.FetchStatus != "SUCCESS" || o.ExtractionStatus == "FAILED" || now.Sub(o.ObservedAt) > 7*24*time.Hour || o.ObservedAt.After(now.Add(time.Minute)) {
			failed = true
			continue
		}
		usable = true
		apply := false
		end := false
		for _, e := range es {
			if e.ObservationID != o.ID || e.Confidence < 0.8 {
				continue
			}
			a.EvidenceIDs = append(a.EvidenceIDs, e.ID)
			switch e.Type {
			case "APPLY_ACTION":
				apply = apply || (e.Value == "PRESENT" && d.PositiveApplication(o.Text) && d.PositiveApplication(e.Excerpt))
			case "CLOSED_SIGNAL":
				end = end || (e.Value == "CLOSED" && d.NegativeApplication(o.Text) && d.NegativeApplication(e.Excerpt))
			case "DEADLINE":
				t, err := d.DeadlineInstant(e.Value, o.Timezone)
				end = end || (err == nil && !now.Before(t))
			}
		}
		closed = closed || end
		open = open || (apply && !end)
	}
	switch {
	case open && closed:
		a.Status = "NEEDS_VERIFICATION"
		a.Reason = "Recent official sources conflict"
	case closed:
		a.Status = "CLOSED"
		a.Reason = "Official explicit closure or elapsed deadline"
	case failed:
		a.Reason = "Latest official fetch failed or is stale; absence is not closure"
	case open:
		a.Status = "OPEN"
		a.Reason = "Recent official observation has an application action and no closure evidence"
	case usable:
		a.Status = "NEEDS_VERIFICATION"
		a.Reason = "Official page lacks decisive application evidence"
	case !official && len(os) > 0:
		allFailed := true
		for _, o := range os {
			if o.FetchStatus == "SUCCESS" {
				allFailed = false
			}
		}
		if !allFailed {
			a.Status = "NEEDS_VERIFICATION"
			a.Reason = "Only non-official evidence is available"
		}
	}
	return a
}
func byType(es []d.Evidence, t string) []d.Evidence {
	out := []d.Evidence{}
	for _, e := range es {
		if e.Type == t && e.Confidence >= 0.8 {
			out = append(out, e)
		}
	}
	return out
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if d.Normalize(x) == d.Normalize(v) {
			return true
		}
	}
	return false
}
func alternatives(value string, choices []string) bool {
	for _, v := range strings.Split(value, "|") {
		if contains(choices, v) {
			return true
		}
	}
	return false
}
func Eligibility(j d.Job, p d.Profile, es []d.Evidence, now time.Time) d.Eligibility {
	a := d.Eligibility{ID: d.ID(), JobID: j.ID, UserID: p.UserID, Status: "ELIGIBLE", RuleVersion: d.RuleVersion, AssessedAt: now, Results: []d.RuleResult{}}
	types := []string{"GRADUATION_REQUIREMENT", "EDUCATION_REQUIREMENT", "JOB_TYPE", "LOCATION", "EXPERIENCE_REQUIREMENT", "MAJOR_REQUIREMENT", "LANGUAGE_REQUIREMENT", "TECH_STACK"}
	critical := map[string]bool{"GRADUATION_REQUIREMENT": true, "EDUCATION_REQUIREMENT": true, "JOB_TYPE": true}
	for _, typ := range types {
		ev := byType(es, typ)
		if typ == "TECH_STACK" {
			hard := []d.Evidence{}
			for _, v := range ev {
				if strings.HasPrefix(v.Value, "REQUIRED:") {
					hard = append(hard, v)
				}
			}
			if len(hard) > 0 {
				ev = hard
			}
		}
		if len(ev) == 0 {
			result := "NOT_APPLICABLE"
			if critical[typ] {
				result = "UNKNOWN"
			}
			a.Results = append(a.Results, d.RuleResult{Rule: typ, Result: result, Explanation: "No sufficiently confident evidence", EvidenceIDs: []string{}})
			continue
		}
		values := map[string]bool{}
		for _, e := range ev {
			values[e.Value] = true
		}
		// Conflicting facts remain unresolved; no arbitrary source or model wins.
		if len(values) > 1 {
			ids := []string{}
			for _, e := range ev {
				ids = append(ids, e.ID)
			}
			a.Results = append(a.Results, d.RuleResult{Rule: typ, Result: "UNKNOWN", Explanation: "Conflicting requirements require verification", EvidenceIDs: ids})
			continue
		}
		e := ev[0]
		r := d.RuleResult{Rule: typ, Requirement: e.Value, Result: "PASS", EvidenceIDs: []string{}, Explanation: "Identified requirement satisfied"}
		for _, v := range ev {
			r.EvidenceIDs = append(r.EvidenceIDs, v.ID)
		}
		fail := func() { r.Result = "FAIL"; r.Explanation = "Explicit requirement is not satisfied" }
		unknown := func() { r.Result = "UNKNOWN"; r.Explanation = "Requirement or candidate value needs clarification" }
		switch typ {
		case "GRADUATION_REQUIREMENT":
			r.Candidate = fmt.Sprint(p.GraduationYear)
			bounds := strings.Split(e.Value, "-")
			lo, err := strconv.Atoi(bounds[0])
			hi := lo
			if len(bounds) == 2 {
				hi, err = strconv.Atoi(bounds[1])
			}
			from, to := p.GraduationYear, p.GraduationYear
			if from == 0 {
				from, to = p.GraduationFrom, p.GraduationTo
			}
			if err != nil || lo < 2000 || hi < lo || from == 0 || to < from {
				unknown()
			} else if to < lo || from > hi {
				fail()
			} else if from < lo || to > hi {
				unknown()
			}
		case "EDUCATION_REQUIREMENT":
			r.Candidate = p.Degree
			levels := map[string]int{"ASSOCIATE": 1, "BACHELOR": 2, "MASTER": 3, "PHD": 4}
			want, got := levels[e.Value], levels[p.Degree]
			if want == 0 || got == 0 {
				unknown()
			} else if got < want {
				fail()
			}
		case "JOB_TYPE":
			r.Candidate = strings.Join(p.PreferredTypes, "|")
			if e.Value != "FULL_TIME" && e.Value != "INTERNSHIP" {
				unknown()
			} else if len(p.PreferredTypes) == 0 {
				unknown()
			} else if !contains(p.PreferredTypes, e.Value) {
				r.Result = "CONDITIONAL"
				r.Explanation = "Job type differs from preference"
			}
		case "LOCATION":
			r.Candidate = strings.Join(p.PreferredCities, "|")
			if alternatives(e.Value, p.PreferredCities) {
			} else if alternatives(e.Value, p.AcceptableCities) {
				r.Result = "CONDITIONAL"
				r.Explanation = "Location is acceptable but not preferred"
			} else {
				r.Result = "CONDITIONAL"
				r.Explanation = "Relocation preference requires confirmation"
			}
		case "EXPERIENCE_REQUIREMENT":
			r.Candidate = fmt.Sprint(p.ExperienceMonths)
			n, err := strconv.Atoi(e.Value)
			if err != nil || n < 0 {
				unknown()
			} else if p.ExperienceMonths < n {
				fail()
			}
		case "MAJOR_REQUIREMENT":
			r.Candidate = strings.Join(p.Majors, "|")
			if len(p.Majors) == 0 {
				unknown()
			} else if !alternatives(e.Value, p.Majors) {
				fail()
			}
		case "LANGUAGE_REQUIREMENT":
			r.Candidate = strings.Join(p.Languages, "|")
			if len(p.Languages) == 0 {
				unknown()
			} else if !alternatives(e.Value, p.Languages) {
				fail()
			}
		case "TECH_STACK":
			r.Candidate = strings.Join(p.Skills, "|")
			value := strings.TrimPrefix(e.Value, "REQUIRED:")
			if !strings.HasPrefix(e.Value, "REQUIRED:") {
				r.Result = "NOT_APPLICABLE"
				r.Explanation = "Technology signal is not an explicit hard constraint"
			} else if len(p.Skills) == 0 {
				unknown()
			} else {
				for _, part := range strings.Split(value, "+") {
					if !alternatives(part, p.Skills) {
						fail()
					}
				}
			}
		}
		a.Results = append(a.Results, r)
	}
	priority := map[string]int{"ELIGIBLE": 0, "CONDITIONAL": 1, "UNKNOWN": 2, "INELIGIBLE": 3}
	for _, r := range a.Results {
		s := map[string]string{"FAIL": "INELIGIBLE", "UNKNOWN": "UNKNOWN", "CONDITIONAL": "CONDITIONAL"}[r.Result]
		if priority[s] > priority[a.Status] {
			a.Status = s
		}
	}
	return a
}
func GoFit(es []d.Evidence) string {
	goSeen, other, flex := false, false, false
	count := 0
	for _, e := range byType(es, "TECH_STACK") {
		count++
		v := strings.ToLower(e.Value)
		has := strings.Contains(v, "golang") || contains(strings.FieldsFunc(v, func(r rune) bool { return r == ':' || r == '|' || r == '+' || r == ' ' }), "go")
		if has {
			goSeen = true
			if strings.Contains(v, "|") {
				flex = true
			}
		} else if strings.HasPrefix(e.Value, "REQUIRED:") {
			other = true
		}
	}
	if goSeen && other {
		return "CONFLICTING"
	}
	if flex {
		return "LANGUAGE_FLEXIBLE"
	}
	if goSeen {
		return "EXPLICIT_GO"
	}
	if count == 0 {
		return "UNKNOWN"
	}
	return "NO_GO_SIGNAL"
}

type Weights map[string]float64

func DefaultWeights() Weights {
	return Weights{"status": 30, "eligibility": 30, "city": 10, "type": 5, "go_fit": 10, "role": 10, "freshness": 5}
}
func Rank(j d.Job, p d.Profile, status string, e d.Eligibility, fit string, now time.Time, w Weights) d.Ranking {
	b := map[string]float64{}
	if status == "OPEN" {
		b["status"] = w["status"]
	}
	if e.Status == "ELIGIBLE" {
		b["eligibility"] = w["eligibility"]
	} else if e.Status == "CONDITIONAL" {
		b["eligibility"] = w["eligibility"] * 0.5
	}
	for _, city := range j.Locations {
		if contains(p.PreferredCities, city) {
			b["city"] = w["city"]
		}
	}
	if contains(p.PreferredTypes, j.JobType) {
		b["type"] = w["type"]
	}
	if fit == "EXPLICIT_GO" {
		b["go_fit"] = w["go_fit"]
	} else if fit == "LANGUAGE_FLEXIBLE" {
		b["go_fit"] = w["go_fit"] * 0.7
	}
	for _, role := range p.TargetRoles {
		if strings.Contains(d.Normalize(j.Title), d.Normalize(role)) {
			b["role"] = w["role"]
		}
	}
	age := now.Sub(j.UpdatedAt).Hours() / 24
	if age >= 0 && age < 7 {
		b["freshness"] = w["freshness"] * (1 - age/7)
	}
	score := 0.0
	keys := []string{}
	for k := range b {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		score += b[k]
	}
	return d.Ranking{BreakdownSources: map[string]string{"status": "EVIDENCE_ASSESSMENT", "eligibility": "EVIDENCE_AND_USER_PROFILE", "go_fit": "EVIDENCE_ASSESSMENT", "city": "JOB_METADATA_AND_USER_PREFERENCE", "type": "JOB_METADATA_AND_USER_PREFERENCE", "role": "JOB_METADATA_AND_USER_PREFERENCE", "freshness": "OBSERVATION_METADATA"}, JobID: j.ID, UserID: p.UserID, Score: score, Breakdown: b, RuleVersion: d.RuleVersion, AssessedAt: now}
}
func Fingerprint(company, title, typ string, locations []string, text string) string {
	return d.Hash(d.JSON(struct {
		Version   int      `json:"version"`
		Company   string   `json:"company"`
		Title     string   `json:"title"`
		Type      string   `json:"job_type"`
		Locations []string `json:"locations"`
		Content   string   `json:"content_hash"`
	}{2, d.Normalize(company), d.Normalize(title), typ, CanonicalLocations(locations), d.Hash(text)}))
}
func CanonicalLocations(locations []string) []string {
	values := map[string]bool{}
	for _, v := range locations {
		values[d.Normalize(v)] = true
	}
	out := make([]string, 0, len(values))
	for v := range values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func Compatible(j d.Job, companyID, title, typ string, locations []string) bool {
	return j.CompanyID == companyID && d.Normalize(j.Title) == d.Normalize(title) && j.JobType == typ && d.JSON(CanonicalLocations(j.Locations)) == d.JSON(CanonicalLocations(locations))
}

func Changes(old, new d.Observation, oldE, newE []d.Evidence) []d.Change {
	out := []d.Change{}
	if old.ID == "" || old.Hash == new.Hash || old.FetchStatus != "SUCCESS" || new.FetchStatus != "SUCCESS" {
		return out
	}
	add := func(t string) {
		out = append(out, d.Change{ID: d.ID(), JobID: new.JobID, From: old.ID, To: new.ID, Type: t, CreatedAt: new.ObservedAt})
	}
	add("JD_CONTENT_CHANGED")
	fields := map[string]string{"GRADUATION_REQUIREMENT": "GRADUATION_CHANGED", "LOCATION": "LOCATION_CHANGED", "APPLY_ACTION": "APPLY_SIGNAL_CHANGED", "DEADLINE": "DEADLINE_CHANGED", "TECH_STACK": "TECH_REQUIREMENT_CHANGED"}
	for typ, change := range fields {
		values := func(es []d.Evidence) string {
			v := []string{}
			for _, e := range es {
				if e.Type == typ {
					v = append(v, e.Value)
				}
			}
			sort.Strings(v)
			return strings.Join(v, ";")
		}
		if values(oldE) != values(newE) {
			add(change)
		}
	}
	return out
}

func ValidateWeights(w Weights) error {
	defaults := DefaultWeights()
	for key, v := range w {
		if _, ok := defaults[key]; !ok || v < 0 || v > 100 {
			return fmt.Errorf("invalid ranking weight %s", key)
		}
	}
	for key := range defaults {
		if _, ok := w[key]; !ok {
			return fmt.Errorf("missing ranking weight %s", key)
		}
	}
	return nil
}
