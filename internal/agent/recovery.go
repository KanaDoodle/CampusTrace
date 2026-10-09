package agent

import (
	"context"
	"errors"
	"net"
	"time"

	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

type ToolFailure struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	Next      string `json:"next_step"`
}

func ClassifyToolFailure(err error) ToolFailure {
	switch {
	case errors.Is(err, p.ErrNotFound):
		return ToolFailure{"NOT_FOUND", false, "核对岗位或记录编号；存在多个候选时请用户选择。"}
	case errors.Is(err, p.ErrConflict):
		return ToolFailure{"STALE_INPUT", false, "资料或记录已变化，请重新查询；不要重放写操作。"}
	case errors.Is(err, p.ErrValidation):
		return ToolFailure{"INVALID_ARGUMENTS", false, "核对参数和允许范围，必要时请用户补充。"}
	case errors.Is(err, context.DeadlineExceeded):
		return ToolFailure{"TOOL_TIMEOUT", false, "查询已超时，缩小范围后重新发起。"}
	case errors.Is(err, context.Canceled):
		return ToolFailure{"CANCELLED", false, "用户或服务已取消本次执行。"}
	}
	var n net.Error
	if errors.As(err, &n) && (n.Timeout() || n.Temporary()) {
		return ToolFailure{"TEMPORARY_UNAVAILABLE", true, "只读查询允许一次有界重试；失败后说明未取得依据。"}
	}
	return ToolFailure{"TOOL_UNAVAILABLE", false, "该工具暂不可用，可以查询其他现有依据或说明缺失。"}
}

// Retry only actual transient read failures, never proposals or paid model calls.
func ExecuteRead(ctx context.Context, tools Toolset, user string, c Call) (any, int, error) {
	v, e := tools.Execute(ctx, user, c.Name, c.Args)
	if e == nil || IsWrite(c.Name) || !ClassifyToolFailure(e).Retryable || ctx.Err() != nil {
		return v, 1, e
	}
	t := time.NewTimer(100 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return nil, 1, ctx.Err()
	case <-t.C:
	}
	v, e = tools.Execute(ctx, user, c.Name, c.Args)
	return v, 2, e
}
