package agent

import (
	"context"
	"encoding/json"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

type SkillRunner struct {
	Store    *p.Store
	Tools    Toolset
	Deadline time.Duration
}

func (r SkillRunner) binding(identity string) string {
	if t, ok := r.Tools.(*Tools); ok {
		retrieval := ""
		if t.RAG != nil {
			retrieval = t.RAG.Options.Identity()
		}
		return d.Hash(identity + d.JSON(t.MatchModel) + t.MaskName + retrieval)
	}
	return identity
}

func (r SkillRunner) Start(ctx context.Context, user string, in SkillInput) (p.AgentExecution, error) {
	if e := in.Validate(); e != nil {
		return p.AgentExecution{}, p.ErrValidation
	}
	// Inputs are replayable local task parameters, never model configurations.
	in.Company = resume.Redact(in.Company)
	in.Topic = resume.Redact(in.Topic)
	if in.SkillID == "daily-review" {
		if e := r.Store.RefreshAgentFeed(ctx, user); e != nil {
			return p.AgentExecution{}, e
		}
	}
	identity, rev, e := r.Store.AgentExecutionIdentity(ctx, user, in.Company, in.JobID, in.SkillID)
	if e != nil {
		return p.AgentExecution{}, e
	}
	identity = r.binding(identity)
	s, _ := FindSkill(in.SkillID)
	calls := []p.ExecutionCall{}
	for _, c := range in.Plan() {
		if Validate(c.Name, c.Args) != nil || IsWrite(c.Name) {
			return p.AgentExecution{}, p.ErrValidation
		}
		calls = append(calls, p.ExecutionCall{ID: c.ID, Name: c.Name, Args: c.Args})
	}
	input := json.RawMessage(d.JSON(in))
	v := p.AgentExecution{SkillID: s.ID, SkillVersion: s.Version, HarnessVersion: HarnessVersion, Input: input, RequestKey: in.RequestKey, Fingerprint: d.Hash(string(input)), Identity: identity, MemoryRevision: rev, Calls: calls}
	v, e = r.Store.BeginAgentExecution(ctx, user, v)
	if e != nil {
		return v, e
	}
	return r.Resume(ctx, user, v.ID)
}
func (r SkillRunner) Resume(parent context.Context, user, id string) (v p.AgentExecution, err error) {
	v, lease, err := r.Store.ClaimAgentExecution(parent, user, id)
	if err != nil || lease == "" {
		return v, err
	}
	duration := r.Deadline
	if duration <= 0 {
		duration = 35 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	defer func() {
		if v.State == "RUNNING" {
			v.State = "PAUSED"
			v.Answer = "本次执行已中断，已完成的读取保留为检查点；可主动继续或取消。"
		}
		if v.State == "PAUSED" && v.Resumes >= p.MaxAgentExecutionAttempts {
			v.State = "FAILED"
			v.Answer = "已达到本次任务的尝试上限。已取得的依据仍可查看；请核对原因后发起新任务。"
		}
		cleanup, done := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
		defer done()
		if e := r.Store.CheckpointAgentExecution(cleanup, user, lease, v); e != nil {
			if err == nil {
				err = e
			}
		} else if err == nil {
			// Return the persisted timestamp and lease-derived continuation state,
			// including a concurrently forgotten/cancelled record, never a guess.
			v, err = r.Store.AgentExecution(cleanup, user, v.ID)
		}
	}()
	var in SkillInput
	if json.Unmarshal(v.Input, &in) != nil {
		return v, p.ErrValidation
	}
	if in.SkillID == "daily-review" {
		if e := r.Store.RefreshAgentFeed(ctx, user); e != nil {
			return v, e
		}
	}
	identity, rev, e := r.Store.AgentExecutionIdentity(ctx, user, in.Company, in.JobID, in.SkillID)
	if e != nil {
		return v, e
	}
	identity = r.binding(identity)
	skill, ok := FindSkill(v.SkillID)
	if !ok || skill.Version != v.SkillVersion || v.HarnessVersion != HarnessVersion || identity != v.Identity {
		v.State = "STALE"
		v.MemoryRevision = rev
		v.Observations = []p.ExecutionObservation{}
		v.Answer = "资料、来源或执行版本已变化。旧检查点未复用，请用当前资料发起新任务。"
		return v, nil
	}
	allowed := map[string]bool{}
	for _, name := range skill.Tools {
		allowed[name] = true
	}
	for v.Next < len(v.Calls) {
		if ctx.Err() != nil {
			return v, nil
		}
		c := v.Calls[v.Next]
		if !allowed[c.Name] || IsWrite(c.Name) || Validate(c.Name, c.Args) != nil {
			return v, p.ErrValidation
		}
		toolCtx, done := context.WithTimeout(ctx, 8*time.Second)
		value, attempts, e := ExecuteRead(toolCtx, r.Tools, user, Call{ID: c.ID, Name: c.Name, Args: c.Args})
		done()
		if e != nil {
			f := ClassifyToolFailure(e)
			v.Failures = append(v.Failures, p.ExecutionFailure{Tool: c.Name, Code: f.Code, Next: f.Next, Attempts: attempts})
			v.State = "PAUSED"
			v.Answer = "任务停在未完成的步骤，之前的读取已保存。请核对失败原因后继续。"
			return v, nil
		}
		mask := ""
		if tools, ok := r.Tools.(*Tools); ok {
			mask = tools.MaskName
		}
		value, e = safeObservation(value, mask)
		if e != nil {
			return v, e
		}
		encoded := d.JSON(value)
		if len(encoded) > 32768 {
			v.Failures = append(v.Failures, p.ExecutionFailure{Tool: c.Name, Code: "OUTPUT_LIMIT", Next: "请缩小查询范围，未接受截断结果。", Attempts: attempts})
			v.State = "PAUSED"
			return v, nil
		}
		v.Observations = append(v.Observations, p.ExecutionObservation{Tool: c.Name, Data: json.RawMessage(encoded)})
		if len(d.JSON(v.Observations)) > 98304 {
			v.Observations = v.Observations[:len(v.Observations)-1]
			v.Failures = append(v.Failures, p.ExecutionFailure{Tool: c.Name, Code: "OUTPUT_LIMIT", Next: "本项资料超过剩余容量，请缩小范围后发起新任务。", Attempts: attempts})
			v.State = "PAUSED"
			return v, nil
		}
		v.Next++
		if e = r.Store.CheckpointAgentExecution(ctx, user, lease, v); e != nil {
			return v, e
		}
	}
	facts := []any{}
	finalIdentity, finalRevision, e := r.Store.AgentExecutionIdentity(ctx, user, in.Company, in.JobID, in.SkillID)
	if e != nil {
		return v, e
	}
	if r.binding(finalIdentity) != v.Identity {
		v.State = "STALE"
		v.MemoryRevision = finalRevision
		v.Observations = []p.ExecutionObservation{}
		v.Answer = "执行期间资料或来源发生变化，请按当前资料重新发起任务。"
		return v, nil
	}
	for _, o := range v.Observations {
		var data any
		json.Unmarshal(o.Data, &data)
		facts = append(facts, map[string]any{"tool": o.Tool, "data": data})
	}
	v.Answer = GroundedAnswer(facts)
	v.State = "COMPLETED"
	return v, nil
}
