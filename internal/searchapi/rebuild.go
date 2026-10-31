package searchapi

import "context"

// Physical index UUIDs remain the fencing token. A rebuild never changes the
// read generation until Core has acknowledged every snapshot/delta at its barrier.
type Rebuild struct {
	Operation string `json:"operation"`
	Phase     string `json:"phase"`
	Mode      string `json:"mode"`
	Source    string `json:"source_generation"`
	Target    string `json:"target_generation"`
	Dataset   string `json:"dataset,omitempty"`
	Version   int64  `json:"cutover_version"`
	Documents int64  `json:"documents"`
}
type RebuildRef struct {
	Operation string `json:"operation"`
	Dataset   string `json:"dataset,omitempty"`
}
type RebuildCommit struct {
	RebuildRef
	Version   int64 `json:"version"`
	Documents int64 `json:"documents"`
}
type OnlineRebuilder interface {
	ClaimRebuild(context.Context, RebuildRef) (Status, error)
	CommitRebuild(context.Context, RebuildCommit) (Status, error)
	AckRebuild(context.Context, RebuildRef) (Status, error)
}

func (c *Client) ClaimRebuild(ctx context.Context, in RebuildRef) (out Status, err error) {
	err = c.call(ctx, "/v1/rebuild/claim", c.WriteToken, in, &out)
	return
}
func (c *Client) CommitRebuild(ctx context.Context, in RebuildCommit) (out Status, err error) {
	err = c.call(ctx, "/v1/rebuild/commit", c.WriteToken, in, &out)
	return
}
func (c *Client) AckRebuild(ctx context.Context, in RebuildRef) (out Status, err error) {
	err = c.call(ctx, "/v1/rebuild/ack", c.WriteToken, in, &out)
	return
}
func (c *Client) AbortRebuild(ctx context.Context, in RebuildRef) (out Status, err error) {
	err = c.call(ctx, "/v1/rebuild/abort", c.WriteToken, in, &out)
	return
}
func (c *Client) Rollback(ctx context.Context) (out Status, err error) {
	err = c.call(ctx, "/v1/rebuild/rollback", c.WriteToken, struct{}{}, &out)
	return
}
