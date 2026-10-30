package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/config"
	"github.com/KanaDoodle/CampusTrace/internal/eventbus"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: events init | replay --from-version N --limit N")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch os.Args[1] {
	case "init":
		cfg, e := eventbus.FromEnv("")
		if e != nil {
			return e
		}
		producer, e := eventbus.NewProducer(cfg)
		if e != nil {
			return e
		}
		defer producer.Close()
		return producer.EnsureTopics(ctx)
	case "replay":
		flags := flag.NewFlagSet("replay", flag.ContinueOnError)
		after := flags.Int64("from-version", 0, "exclusive lower bound")
		limit := flags.Int("limit", 1000, "at most 10000 published events")
		if e := flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected replay argument")
		}
		store, e := p.Open(ctx, config.Load().DSN)
		if e != nil {
			return e
		}
		defer store.DB.Close()
		n, last, e := store.ReplayPublishedJobEvents(ctx, *after, *limit)
		if e == nil {
			fmt.Printf("Queued %d existing events through version %d for idempotent replay\n", n, last)
		}
		return e
	default:
		return fmt.Errorf("unknown event command")
	}
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "event operator action failed")
		os.Exit(1)
	}
}
