// Package matcheval separates source/shape validation from human preference
// agreement. It neither reads live accounts nor writes matching reports.
package matcheval

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

const Version = "campustrace-matching-eval-v1"

type Pair struct {
	Preferred string `json:"preferred"`
	Other     string `json:"other"`
	Relation  string `json:"relation"` // ABOVE or TIED
}
type Reference struct {
	AcceptableTopIDs []string `json:"acceptable_top_job_ids"`
	Pairs            []Pair   `json:"pairs,omitempty"`
	Reason           string   `json:"reason"`
	Reviewed         bool     `json:"reviewed"`
}
type Case struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Source    string                 `json:"source"`
	Candidate matching.Candidate     `json:"candidate"`
	Jobs      []matching.HolisticJob `json:"jobs"`
	Reference Reference              `json:"reference"`
}
type Trial struct {
	CaseID    string                          `json:"case_id"`
	Attempt   int                             `json:"attempt"`
	Model     string                          `json:"model"`
	Report    *matching.HolisticCompanyReport `json:"report,omitempty"`
	ErrorCode string                          `json:"error_code,omitempty"`
	LatencyMS *int64                          `json:"latency_ms,omitempty"`
	Usage     analysis.Usage                  `json:"usage"`
}
type Dataset struct {
	Version  string  `json:"version"`
	Cases    []Case  `json:"cases"`
	Recorded []Trial `json:"recorded_results,omitempty"`
}
type Score struct {
	CaseID        string         `json:"case_id"`
	Attempt       int            `json:"attempt"`
	Source        string         `json:"source"`
	Model         string         `json:"model"`
	ContractPass  bool           `json:"contract_pass"`
	QualityScored bool           `json:"quality_scored"`
	TopAgreement  *bool          `json:"top_agreement,omitempty"`
	PairCorrect   int            `json:"pair_correct"`
	PairTotal     int            `json:"pair_total"`
	ErrorCode     string         `json:"error_code,omitempty"`
	LatencyMS     *int64         `json:"latency_ms,omitempty"`
	Usage         analysis.Usage `json:"usage"`
}
type Summary struct {
	Mode             string  `json:"mode"`
	PromptRevision   string  `json:"prompt_revision"`
	Trials           int     `json:"trial_count"`
	ContractPassed   int     `json:"contract_passed"`
	QualityScored    int     `json:"quality_scored"`
	TopAgreement     int     `json:"top_agreement"`
	PairCorrect      int     `json:"pair_correct"`
	PairTotal        int     `json:"pair_total"`
	MissingUsage     int     `json:"trials_without_token_usage"`
	PromptTokens     int     `json:"known_prompt_tokens"`
	CompletionTokens int     `json:"known_completion_tokens"`
	Scores           []Score `json:"scores"`
	Groups           []Group `json:"groups"`
	Notice           string  `json:"notice"`
}

type Group struct {
	Source         string `json:"source"`
	Model          string `json:"model"`
	Trials         int    `json:"trials"`
	ContractPassed int    `json:"contract_passed"`
	QualityScored  int    `json:"quality_scored"`
	TopAgreement   int    `json:"top_agreement"`
	PairCorrect    int    `json:"pair_correct"`
	PairTotal      int    `json:"pair_total"`
	LatencyTrials  int    `json:"latency_trials"`
	P50MS          *int64 `json:"p50_ms,omitempty"`
	P95MS          *int64 `json:"p95_ms,omitempty"`
}

func (d Dataset) Validate() error {
	if d.Version != Version || len(d.Cases) == 0 || len(d.Cases) > 64 {
		return errors.New("invalid evaluation dataset version or case count")
	}
	seen := map[string]bool{}
	for _, c := range d.Cases {
		if c.ID == "" || len(c.ID) > 128 || seen[c.ID] || len(c.Name) > 500 || !oneOf(c.Source, "SYNTHETIC", "USER_REVIEWED_SNAPSHOT") {
			return errors.New("invalid or duplicate evaluation case")
		}
		seen[c.ID] = true
		if err := matching.ValidateHolisticInput(c.Candidate, c.Jobs, true); err != nil {
			return fmt.Errorf("case %s exceeds company comparison input limits", c.ID)
		}
		if err := ValidateReference(c.Reference, c.Jobs); err != nil {
			return fmt.Errorf("case %s: %w", c.ID, err)
		}
	}
	if len(d.Recorded) > 192 {
		return errors.New("too many recorded trials")
	}
	keys := map[string]bool{}
	for _, r := range d.Recorded {
		key := fmt.Sprintf("%s/%d/%s", r.CaseID, r.Attempt, r.Model)
		if !seen[r.CaseID] || r.Attempt < 1 || r.Attempt > 3 || len(r.Model) > 300 || keys[key] {
			return errors.New("invalid or duplicate recorded trial")
		}
		if r.LatencyMS != nil && *r.LatencyMS < 0 || r.Usage.PromptTokens < 0 || r.Usage.CompletionTokens < 0 {
			return errors.New("invalid recorded latency or token usage")
		}
		keys[key] = true
	}
	return nil
}
func ValidateReference(r Reference, jobs []matching.HolisticJob) error {
	ids := map[string]bool{}
	for _, j := range jobs {
		ids[j.ID] = true
	}
	if len(r.AcceptableTopIDs) > len(jobs) || len(r.Pairs) > 120 || len(r.Reason) > 6000 {
		return errors.New("invalid human reference size")
	}
	seen := map[string]bool{}
	for _, id := range r.AcceptableTopIDs {
		if !ids[id] || seen[id] {
			return errors.New("human reference points outside the comparison scope")
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, p := range r.Pairs {
		key := p.Preferred + "/" + p.Other
		reverse := p.Other + "/" + p.Preferred
		if !ids[p.Preferred] || !ids[p.Other] || p.Preferred == p.Other || !oneOf(p.Relation, "ABOVE", "TIED") || seen[key] || seen[reverse] {
			return errors.New("invalid or duplicate pairwise reference")
		}
		seen[key] = true
	}
	if r.Reviewed && (strings.TrimSpace(r.Reason) == "" || len(r.AcceptableTopIDs) == 0) {
		return errors.New("reviewed reference needs an acceptable first choice and a reason")
	}
	return nil
}
func oneOf(v string, choices ...string) bool {
	for _, c := range choices {
		if v == c {
			return true
		}
	}
	return false
}

// A failed/invalid model output is a failed trial for a labeled case, not an
// omitted trial that would inflate agreement. Unlabeled cases have no quality
// score, even when all exact source citations validate.
func Grade(c Case, t Trial) Score {
	s := Score{CaseID: c.ID, Attempt: t.Attempt, Source: c.Source, Model: t.Model, ErrorCode: t.ErrorCode, LatencyMS: t.LatencyMS, Usage: t.Usage}
	s.ContractPass = t.ErrorCode == "" && t.Report != nil && matching.ValidateCompanyReport(*t.Report, c.Candidate, c.Jobs) == nil
	if !s.ContractPass && s.ErrorCode == "" {
		s.ErrorCode = "REPORT_INVALID"
	}
	s.QualityScored = c.Reference.Reviewed && len(c.Reference.AcceptableTopIDs) > 0
	if !s.QualityScored {
		return s
	}
	agree := false
	ranks := map[string]int{}
	if s.ContractPass {
		for _, v := range t.Report.Choices {
			ranks[v.ID] = v.Rank
		}
		// Every joint first choice must be acceptable, rather than crediting a
		// model that labels all jobs as first and happens to include the right one.
		agree = true
		for _, v := range t.Report.Choices {
			if v.Rank == 1 && !oneOf(v.ID, c.Reference.AcceptableTopIDs...) {
				agree = false
			}
		}
	}
	s.TopAgreement = &agree
	s.PairTotal = len(c.Reference.Pairs)
	for _, p := range c.Reference.Pairs {
		if s.ContractPass && (p.Relation == "ABOVE" && ranks[p.Preferred] < ranks[p.Other] || p.Relation == "TIED" && ranks[p.Preferred] == ranks[p.Other]) {
			s.PairCorrect++
		}
	}
	return s
}
func Summarize(mode string, scores []Score) Summary {
	s := Summary{Mode: mode, PromptRevision: matching.HolisticPromptRevision, Trials: len(scores), Scores: scores, Notice: "原文核对通过不等于匹配准确；首选与成对排序只衡量对人工标注的认可程度，不代表录用概率。合成案例用于验证评分器，不能当作真实模型效果。未返回 usage 的请求不计入已知 Token 总量。"}
	groups := map[string]*Group{}
	latencies := map[string][]int64{}
	for _, v := range scores {
		key := v.Source + "\n" + v.Model
		g := groups[key]
		if g == nil {
			g = &Group{Source: v.Source, Model: v.Model}
			groups[key] = g
		}
		g.Trials++
		g.PairCorrect += v.PairCorrect
		g.PairTotal += v.PairTotal
		if v.ContractPass {
			g.ContractPassed++
		}
		if v.QualityScored {
			g.QualityScored++
			if v.TopAgreement != nil && *v.TopAgreement {
				g.TopAgreement++
			}
		}
		if v.LatencyMS != nil {
			latencies[key] = append(latencies[key], *v.LatencyMS)
		}
		if v.ContractPass {
			s.ContractPassed++
		}
		if v.QualityScored {
			s.QualityScored++
			if v.TopAgreement != nil && *v.TopAgreement {
				s.TopAgreement++
			}
		}
		s.PairCorrect += v.PairCorrect
		s.PairTotal += v.PairTotal
		if v.Usage.Known {
			s.PromptTokens += v.Usage.PromptTokens
			s.CompletionTokens += v.Usage.CompletionTokens
		} else {
			s.MissingUsage++
		}
	}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		g := groups[key]
		times := latencies[key]
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		g.LatencyTrials = len(times)
		if len(times) > 0 {
			p50, p95 := times[(len(times)-1)/2], times[(len(times)*95+99)/100-1]
			g.P50MS = &p50
			g.P95MS = &p95
		}
		s.Groups = append(s.Groups, *g)
	}
	return s
}
