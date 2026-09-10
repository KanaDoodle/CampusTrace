package main

import (
	"flag"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"log"
)

func main() {
	redrive := flag.String("redrive", "", "explicit task ID to redrive")
	flag.Parse()
	ctx, cancel := bootstrap.Root()
	defer cancel()
	a, err := bootstrap.Open(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	if *redrive != "" {
		if err = a.Queue.Redrive(ctx, *redrive); err != nil {
			log.Fatal(err)
		}
		fmt.Println("redriven", *redrive)
		return
	}
	v, err := a.Queue.DLQ(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(d.JSON(v))
}
