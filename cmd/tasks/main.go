// tasks initializes Kafka topics or transfers a stopped Redis task backlog.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/taskmigration"
	"log"
	"os"
	"time"
)

func main() {
	migrate := flag.Bool("migrate-redis", false, "import existing Redis task backlog; leave old keys untouched")
	stopped := flag.Bool("writers-stopped", false, "confirm all old task producers and workers have stopped")
	flag.Parse()
	if *migrate && !*stopped {
		log.Fatal("stop all old API/worker/task producers, back up MySQL and Redis, then pass -writers-stopped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	app, e := bootstrap.Open(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer app.Close()
	if e = app.Store.Migrate(ctx); e != nil {
		log.Fatal(e)
	}
	if e = app.Queue.Broker.Init(ctx); e != nil {
		log.Fatal(e)
	}
	if *migrate {
		report, e := taskmigration.Run(ctx, app.Queue)
		if e != nil {
			log.Fatal(e)
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		return
	}
	if e = app.Queue.Broker.EnsureTopics(ctx); e != nil {
		log.Fatal(e)
	}
	log.Print("Kafka task topics initialized")
}
