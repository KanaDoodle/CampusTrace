package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
)

func preparationSource(kind, title, text, id, application string) d.PreparationSource {
	return d.PreparationSource{ID: "s_" + d.Hash(kind + id + text)[:24], Kind: kind, Title: title, Text: text, RecordID: id, ApplicationID: application, Hash: d.Hash(text)}
}
func (r *PreparationRunner) materials(ctx context.Context, user string, in d.PreparationInput, mask string) ([]d.PreparationSource, []string, error) {
	dossier, err := r.Store.ApplicationDossier(ctx, user, in.ApplicationID)
	if err != nil {
		return nil, nil, err
	}
	sources := []d.PreparationSource{}
	missing := []string{}
	tools := Tools{MaskName: mask}
	add := func(kind, title, text, id string) {
		if strings.TrimSpace(text) == "" {
			missing = append(missing, title+"未留档")
			return
		}
		sources = append(sources, preparationSource(kind, tools.memoryText(title), tools.memoryText(text), id, in.ApplicationID))
	}
	if dossier.Context == nil {
		missing = append(missing, "这条历史申请没有冻结 JD、简历和项目材料；当前资料不作为历史提交材料")
	} else {
		c := dossier.Context
		if c.Phase != "SUBMITTED" {
			missing = append(missing, "当前为计划阶段留档，尚不能说明实际提交了哪些材料")
		}
		add("JD", "留档岗位："+c.Job.Company+" / "+c.Job.Title, c.JD, c.Key)
		add("RESUME", "本次留档简历正文", c.ResumeText, c.Key)
		add("PROJECT", "本次留档项目与个人材料", c.CandidateDocument, c.Key)
	}
	reviews, err := r.Store.PreparationReviews(ctx, user, in.ApplicationID, "")
	if err != nil {
		return nil, nil, err
	}
	if len(reviews) == 0 {
		missing = append(missing, "本申请尚无面试复盘；不能据此推断本申请的失分项")
	}
	for _, review := range reviews {
		text := "实际问题：" + strings.Join(review.Questions, "；") + "\n遗漏点：" + strings.Join(review.Missed, "；") + "\n自评：" + review.Evaluation + "\n后续：" + review.FollowUp
		add("HISTORY", "本申请面试复盘", text, review.ID)
	}
	for _, notice := range dossier.Notices {
		if notice.Confirmed != nil {
			add("NOTICE", "本申请已确认招聘通知", notice.Parsed.Text, notice.ID)
		}
	}
	if len(d.JSON(sources)) > 98304 {
		return nil, nil, p.ErrValidation
	}
	return sources, missing, nil
}

// This toolset delegates scoped lookup and BM25 to existing Store/RAG APIs. No
// write tool, current résumé tool, remote MCP, embedding or matching call exists.
type preparationToolset struct {
	tools *Tools
	input d.PreparationInput
}

func preparationDefinitions() []Definition {
	return []Definition{
		{Name: "search_preparation_knowledge", Description: "Search owned learning notes by keywords. Results are learning references, never evidence of personal experience. At most three searches, including rewrites.", Parameters: object(map[string]any{"query": str()}, "query")},
		{Name: "search_related_reviews", Description: "Search up to 50 owned historical interview reviews. Requires the user's other_reviews scope; preserves the original application ID, never implies it happened in this application.", Parameters: object(map[string]any{"query": str()}, "query")},
		{Name: "finish_preparation_plan", Description: "Finish with a structured, time-bounded actionable plan. JD and HISTORY items require literal quotations from corresponding sources. GENERAL items are advice. Revised items must preserve prior IDs and titles and omit mastered/completed items.", Parameters: preparationPlanSchema()},
		{Name: "ask_preparation_question", Description: "Ask one critical missing-information question and pause without inventing data. Do not ask for information already present.", Parameters: object(map[string]any{"question": str()}, "question")},
	}
}

// Publish the constraints already enforced by ValidatePreparationPlan before
// generation, so revision choices are limited to the actual remaining items.
func preparationDefinitionsForState(state *d.PreparationState) []Definition {
	defs := preparationDefinitions()
	items := defs[2].Parameters["properties"].(map[string]any)["items"].(map[string]any)
	fields := items["items"].(map[string]any)["properties"].(map[string]any)
	fields["id"].(map[string]any)["pattern"] = preparationItemIDPattern
	fields["id"].(map[string]any)["maxLength"] = 40
	fields["id"].(map[string]any)["description"] = "Stable internal ID: ASCII letters, digits, underscores or hyphens only. Put Chinese display text in title."
	// A complete tool call must fit the shared 2048 output-token envelope.
	// Keep accepted historical plans compatible; publish compact generation
	// constraints to the model rather than silently truncating its response.
	defs[2].Parameters["properties"].(map[string]any)["summary"].(map[string]any)["maxLength"] = 120
	items["maxItems"] = min(4, state.Input.Minutes/5)
	fields["action"].(map[string]any)["maxLength"] = 80
	fields["deliverable"].(map[string]any)["maxLength"] = 40
	references := fields["references"].(map[string]any)
	references["maxItems"] = 2
	references["items"].(map[string]any)["properties"].(map[string]any)["quote"].(map[string]any)["maxLength"] = 50
	fields["minutes"].(map[string]any)["maximum"] = state.Input.Minutes
	fields["day"].(map[string]any)["maximum"] = state.Input.Days
	if state.PreviousPlan == nil {
		return defs
	}
	excluded := map[string]bool{}
	for _, id := range append(append([]string{}, state.Input.Mastered...), state.Input.Completed...) {
		excluded[id] = true
	}
	ids, titles, bases := []string{}, []string{}, []string{}
	seenTitles, seenBases := map[string]bool{}, map[string]bool{}
	for _, item := range state.PreviousPlan.Items {
		if !excluded[item.ID] {
			ids = append(ids, item.ID)
			if !seenTitles[item.Title] {
				titles = append(titles, item.Title)
				seenTitles[item.Title] = true
			}
			if !seenBases[item.Basis] {
				bases = append(bases, item.Basis)
				seenBases[item.Basis] = true
			}
		}
	}
	items["maxItems"] = min(4, state.Input.Minutes/5, len(ids))
	if len(ids) > 0 {
		fields["id"].(map[string]any)["enum"] = ids
		fields["title"].(map[string]any)["enum"] = titles
		fields["basis"].(map[string]any)["enum"] = bases
	}
	return defs
}
func preparationPlanSchema() map[string]any {
	reference := object(map[string]any{"source_id": str(), "quote": str()}, "source_id", "quote")
	item := object(map[string]any{"id": str(), "title": str(), "minutes": map[string]any{"type": "integer", "minimum": 5, "maximum": 1440}, "day": map[string]any{"type": "integer", "minimum": 1, "maximum": 30}, "basis": map[string]any{"type": "string", "enum": []string{"JD", "HISTORY", "GENERAL"}}, "action": str(), "deliverable": str(), "references": map[string]any{"type": "array", "items": reference, "maxItems": 8}}, "id", "title", "minutes", "day", "basis", "action", "deliverable", "references")
	return object(map[string]any{"summary": str(), "items": map[string]any{"type": "array", "items": item, "minItems": 0, "maxItems": 12}}, "summary", "items")
}
func (t preparationToolset) Definitions() []Definition { return preparationDefinitions() }
func (t preparationToolset) Execute(ctx context.Context, user, name string, args json.RawMessage) (any, error) {
	var a struct {
		Query string `json:"query"`
	}
	if d.Strict(args, &a) != nil || len(strings.TrimSpace(a.Query)) == 0 || len(a.Query) > 1000 {
		return nil, p.ErrValidation
	}
	switch name {
	case "search_preparation_knowledge":
		if t.tools.RAG == nil {
			return nil, p.ErrBackendUnavailable
		}
		// Existing RAG service with keyword options: no implicit paid retrieval.
		local := *t.tools.RAG
		local.Options = rag.Options{}
		found, err := local.SearchDetailed(ctx, user, a.Query, 5)
		if err != nil {
			return nil, err
		}
		sources := []d.PreparationSource{}
		for _, hit := range found.Hits {
			sources = append(sources, preparationSource("KNOWLEDGE", t.tools.memoryText(hit.Title), t.tools.memoryText(hit.Text), hit.ID, ""))
		}
		return sources, nil
	case "search_related_reviews":
		if !t.input.OtherReviews {
			return nil, p.ErrValidation
		}
		rows, err := t.tools.Store.RelatedPreparationReviews(ctx, user)
		if err != nil {
			return nil, err
		}
		type scored struct {
			source d.PreparationSource
			score  int
		}
		ranked := []scored{}
		for _, row := range rows {
			text := strings.Join(row.Review.Questions, "；") + "\n遗漏点：" + strings.Join(row.Review.Missed, "；") + "\n自评：" + row.Review.Evaluation
			score := 0
			for _, term := range rag.Tokens(a.Query) {
				if strings.Contains(strings.ToLower(text), term) {
					score++
				}
			}
			if score > 0 {
				ranked = append(ranked, scored{preparationSource("HISTORY", "历史面试复盘（原申请 "+row.ApplicationID+")", t.tools.memoryText(text), row.Review.ID, row.ApplicationID), score})
			}
		}
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
		sources := []d.PreparationSource{}
		for _, v := range ranked[:min(3, len(ranked))] {
			sources = append(sources, v.source)
		}
		return sources, nil
	}
	return nil, fmt.Errorf("%w: tool outside preparation scope", p.ErrValidation)
}
