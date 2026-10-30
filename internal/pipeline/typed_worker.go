package pipeline

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/redis/go-redis/v9"
	"time"
)

type analysisUncertain struct{}

func (analysisUncertain) Error() string   { return "analysis outcome unknown; manual review required" }
func (analysisUncertain) Transient() bool { return false }

var ErrAnalysisUncertain error = analysisUncertain{}

func (w *Worker) bridgeLegacy(ctx context.Context) {
	consumer := "bridge-" + d.ID()
	pending := []redis.XMessage{}
	for ctx.Err() == nil {
		if len(pending) == 0 {
			recovered, e := w.Queue.Recover(ctx, consumer, w.ClaimIdle)
			if e == nil {
				pending = append(pending, recovered...)
			}
			if len(pending) == 0 {
				streams, e := w.Queue.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: w.Queue.Group(), Consumer: consumer, Streams: []string{w.Queue.Stream(), ">"}, Count: 32, Block: time.Second}).Result()
				if e == nil {
					for _, stream := range streams {
						pending = append(pending, stream.Messages...)
					}
				}
			}
		}
		remaining := pending[:0]
		for _, m := range pending {
			moved, e := w.Queue.BridgeLegacy(ctx, m, consumer)
			if e != nil || !moved {
				remaining = append(remaining, m)
				if e != nil {
					w.Metrics.Add("legacy_transfer_errors_total", 1)
				}
			} else {
				w.Metrics.Add("legacy_transferred_total", 1)
			}
		}
		pending = remaining
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
