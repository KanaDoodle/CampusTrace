// Package demo provides synthetic fixtures. It never fetches sources or calls a model.
package demo

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"time"
)

const Company = "合成青杉科技"
const ModelName = "synthetic-journey-fixture"

func SeedJourney(ctx context.Context, s *p.Store, user string) error {
	if e := s.SaveProfile(ctx, user, d.Profile{Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}, PreferredCities: []string{"上海"}, Degree: "MASTER", GraduationYear: 2027}); e != nil {
		return e
	}
	if _, e := s.SaveProject(ctx, user, d.Project{Name: "合成任务调度服务", Description: "使用 Go 实现任务服务，通过数据库事务、幂等键和重试处理消费中断。", Bullets: []string{"使用合成测试验证恢复过程；尚未验证真实生产负载。"}}); e != nil {
		return e
	}
	ids := []string{}
	descriptions := []struct{ title, text string }{{"Go 后端开发", "熟悉 Go，了解数据库事务。负责业务接口与任务服务开发。"}, {"平台后端开发", "熟悉 Go，了解消息队列。负责平台任务调度与故障恢复。"}, {"基础架构开发", "熟悉服务端开发。负责基础框架、容量分析与工程可靠性。"}}
	for _, j := range descriptions {
		o, e := s.IngestForUser(ctx, user, p.Ingest{Company: Company, Title: j.title, ExternalID: d.ID(), JobType: "FULL_TIME", Locations: []string{"上海"}, Text: j.text, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		if e != nil {
			return e
		}
		ids = append(ids, o.JobID)
	}
	job, e := s.Job(ctx, ids[0])
	if e != nil {
		return e
	}
	if _, e = s.SaveCampaign(ctx, user, "", p.ApplicationCampaign{CompanyID: job.CompanyID, Name: "合成 2027 秋招", Limit: 2, JobIDs: ids, Confirmed: true, RuleURL: "https://example.invalid/synthetic-rule"}); e != nil {
		return e
	}
	identity := matching.ModelIdentity("server-default", ModelName)
	snapshot, e := s.MatchSnapshot(ctx, user, identity, "", ids)
	if e != nil {
		return e
	}
	for _, row := range snapshot.Jobs {
		h := matching.HolisticAssessment{Version: matching.HolisticVersion, Fit: "RELATED", Summary: "合成演示结论：Go 与可靠性实践相关，需核对实际工作内容。", CoreWork: "后端与平台工程", Strengths: []matching.HolisticFinding{}, Gaps: []matching.HolisticFinding{}, Blockers: []matching.HolisticFinding{}, Questions: []string{"请向招聘方核对团队工作范围。"}, NextSteps: []string{"准备事务、幂等与故障恢复的项目讲解。"}, IgnoredFactors: []string{}, Gates: []matching.HolisticGate{}}
		r := matching.Result{QualityVersion: matching.QualityVersion, Holistic: &h, JobID: row.Job.ID, InputKey: row.InputKey, RequirementsKey: row.RequirementsKey, CandidateHash: snapshot.CandidateHash, Model: identity, AnalyzedAt: time.Now().UTC(), Requirements: []matching.Requirement{}, Matches: []matching.Match{}, CandidateFacts: snapshot.Candidate.Facts, Qualifications: matching.Qualification(row.Job, snapshot.Profile, nil, time.Now().UTC()), Breakdown: []matching.SectionScore{}}
		if e = s.SaveMatchResult(ctx, user, "", r); e != nil {
			return e
		}
	}
	jobs := p.WholeJobs(snapshot.Jobs)
	choices := []matching.CompanyChoice{}
	for i, id := range ids {
		var text string
		for _, j := range jobs {
			if j.ID == id {
				text = j.Text
			}
		}
		choices = append(choices, matching.CompanyChoice{ID: id, Rank: i + 1, Reason: "合成参考排序；用于演示批次名额流程。", Advantage: "可讨论 Go 工程实践的迁移。", Tradeoff: "需要核对具体业务与基础设施职责。", JobExcerpt: text, Evidence: []matching.Citation{}})
	}
	report := matching.HolisticCompanyReport{Version: matching.HolisticVersion, Company: Company, InputKey: matching.CompanyInputKey(snapshot.Candidate, jobs, identity), CandidateHash: snapshot.CandidateHash, Model: identity, Summary: "这是预置合成比较，用来演示两名额的选岗流程，不代表真实模型准确率。", Choices: choices, Questions: []string{}}
	return s.SaveCompanyReport(ctx, user, "", report, jobs)
}
