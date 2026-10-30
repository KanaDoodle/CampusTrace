// journey-demo only starts against an explicitly selected empty local demo database.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	"github.com/KanaDoodle/CampusTrace/internal/demo"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	dsn := flag.String("dsn", "", "explicit empty loopback demo database")
	redisAddr := flag.String("redis", "", "disposable Redis loopback address")
	httpAddr := flag.String("http", "127.0.0.1:23380", "loopback HTTP address")
	flag.Parse()
	cfg, e := mysql.ParseDSN(*dsn)
	if e != nil || cfg == nil || cfg.Net != "tcp" || !strings.HasPrefix(cfg.Addr, "127.0.0.1:") || cfg.DBName != "campustrace_journey_demo" {
		log.Fatal("requires an explicit empty 127.0.0.1 demo database named campustrace_journey_demo")
	}
	if !strings.HasPrefix(*redisAddr, "127.0.0.1:") || !strings.HasPrefix(*httpAddr, "127.0.0.1:") {
		log.Fatal("demo Redis and HTTP must use loopback")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, e := p.Open(ctx, *dsn)
	check(e)
	defer s.DB.Close()
	var count int
	check(s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&count))
	if count != 0 {
		log.Fatal("demo database is not empty; refusing to modify it")
	}
	check(s.Migrate(ctx))
	secret := make([]byte, 32)
	_, e = rand.Read(secret)
	check(e)
	authn := auth.Service{Store: s, Secret: secret}
	u, e := authn.Register(ctx, "demo@campustrace.invalid", "Synthetic2026!")
	check(e)
	check(demo.SeedJourney(ctx, s, u))
	r := redis.NewClient(&redis.Options{Addr: *redisAddr})
	defer r.Close()
	check(r.Ping(ctx).Err())
	q := &pipeline.Queue{R: r, Prefix: "journey-demo:"}
	metrics := observability.New()
	tools := &agent.Tools{Store: s, Queue: q, RAG: &rag.Service{Store: s}}
	runtime := &agent.Runtime{Store: s, Tools: tools, Model: agent.DemoModel{}, R: r, Prefix: q.Prefix, MaxModels: 4, MaxTools: 8, Deadline: 35 * time.Second, ToolTimeout: 8 * time.Second}
	api := &transport.API{Store: s, Queue: q, Auth: authn, Tools: tools, Agent: runtime, Metrics: metrics, ResumeModelName: demo.ModelName}
	listener, e := net.Listen("tcp", *httpAddr)
	check(e)
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	fmt.Printf("Synthetic demo: http://%s\nAccount: demo@campustrace.invalid\nPassword: Synthetic2026!\nNo external model or source requests are configured.\n", listener.Addr())
	e = server.Serve(listener)
	if e != nil && e != http.ErrServerClosed {
		check(e)
	}
}
func check(e error) {
	if e != nil {
		log.Fatal(e)
	}
}
