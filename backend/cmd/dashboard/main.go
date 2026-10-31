// dashboard: backend for the Kindle e-ink dashboard.
//
//	dashboard token            print a new ACCESS_TOKEN
//	dashboard migrate          create or update tables
//	dashboard import           load data/ into the content tables
//	dashboard collect          fetch every source once and rebuild the snapshot
//	dashboard seed             add dummy practice history (for trying it out)
//	dashboard serve            migrate + import + collect, then serve and collect on a timer
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shyam/dashboard/internal/collector"
	"github.com/shyam/dashboard/internal/config"
	"github.com/shyam/dashboard/internal/db"
	"github.com/shyam/dashboard/internal/importer"
	"github.com/shyam/dashboard/internal/server"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if cmd == "token" {
		fmt.Println(config.NewToken())
		return
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	col := collector.New(cfg, pool)

	switch cmd {
	case "migrate":
		log.Println("migrations applied")
	case "import":
		must(importer.Run(ctx, pool, cfg.DataDir))
		_, err := col.Build(ctx)
		must(err)
	case "seed":
		must(importer.Run(ctx, pool, cfg.DataDir))
		must(col.SeedDummyState(ctx))
		_, err := col.Build(ctx)
		must(err)
		log.Println("dummy history seeded")
	case "collect":
		must(col.Collect(ctx))
	case "serve":
		must(importer.Run(ctx, pool, cfg.DataDir))
		if err := col.Collect(ctx); err != nil {
			log.Printf("initial collect: %v", err)
		}
		go func() {
			t := time.NewTicker(cfg.CollectEvery)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
					if err := col.Collect(cctx); err != nil {
						log.Printf("collect: %v", err)
					}
					cancel()
				}
			}
		}()
		srv, err := server.New(cfg, col)
		must(err)
		must(srv.ListenAndServe(ctx))
	default:
		log.Fatalf("unknown command %q (token|migrate|import|seed|collect|serve)", cmd)
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
