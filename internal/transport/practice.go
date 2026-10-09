package transport

import (
	"context"
	"errors"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/practice"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"net/http"
	"time"
)

func (a *API) practiceRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/practice/capabilities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		write(w, map[string]any{"enabled": a.Practice.Ready(r.Context()), "policy": practice.Policy, "deadline_seconds": 45, "memory_mb": 384, "network": false}, nil)
	})
	on("GET /api/practice/runs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := a.Store.PracticeRuns(r.Context(), user(r))
		write(w, v, e)
	})
	on("POST /api/practice/runs", func(w http.ResponseWriter, r *http.Request) {
		var in practice.Input
		if decode(r, &in) != nil || practice.Validate(in.Code, in.Tests) != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		previous, e := a.Store.PracticeReceipt(r.Context(), user(r), in)
		if e == nil {
			write(w, previous, nil)
			return
		}
		if !errors.Is(e, p.ErrNotFound) {
			write(w, nil, e)
			return
		}
		if !a.Practice.Ready(r.Context()) {
			codedError(w, http.StatusServiceUnavailable, "PRACTICE_UNAVAILABLE")
			return
		}
		out, fresh, e := a.Store.BeginPractice(r.Context(), user(r), in)
		if e != nil || !fresh {
			write(w, out, e)
			return
		}
		if a.Queue != nil {
			allowed, err := a.Queue.Allow(r.Context(), "practice:user:"+user(r), 20, time.Hour)
			if err != nil || !allowed {
				out.State = "ERROR"
				out.Output = "本小时练习次数已达上限，或暂时无法检查运行额度。"
				e = a.Store.FinishPractice(r.Context(), user(r), out)
				write(w, out, e)
				return
			}
		}
		result, e := a.Practice.Run(r.Context(), practice.Request{ID: out.ID, Code: in.Code, Tests: in.Tests})
		if e != nil {
			result = practice.Result{State: "ERROR", ExitCode: -1, Output: "练习组件未返回完整结果；需要重新运行时，请发起新的练习。", Policy: practice.Policy}
		}
		result.Output = resume.Redact(result.Output)
		if len(result.Output) > practice.MaxOutput {
			result.Output = "输出包含需要遮盖的信息，长度超过展示上限。"
			result.Truncated = true
		}
		out.Result = result
		cleanup, done := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer done()
		e = a.Store.FinishPractice(cleanup, user(r), out)
		write(w, out, e)
	})
}
