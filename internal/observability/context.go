package observability

import "context"

type key struct{}
type Fields struct{ RequestID, TaskID, JobID, RunID string }

func With(ctx context.Context, fields Fields) context.Context {
	return context.WithValue(ctx, key{}, fields)
}
func From(ctx context.Context) Fields { v, _ := ctx.Value(key{}).(Fields); return v }
