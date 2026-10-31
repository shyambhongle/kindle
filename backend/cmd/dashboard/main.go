// dashboard: backend for the Kindle e-ink dashboard.
//
//	dashboard token            print a new ACCESS_TOKEN
//	dashboard url              print the Kindle URL for the token now in .env
//	dashboard migrate          create or update tables
//	dashboard import           load data/ into the content tables
//	dashboard collect          fetch every source once and rebuild the snapshot
//	dashboard backup           write a compressed pg_dump into BACKUP_DIR
//	dashboard notion-inspect   print the Notion board schema and how each task maps
//	dashboard german-generate  [day N | week N] generate the German quiz or Saturday test now
//	dashboard english-generate [YYYY-MM-DD]     generate the English daily pack now
//	dashboard summary-generate [N]              generate the book summary for plan day N (default today)
//	dashboard seed             add dummy practice history (for trying it out)
//	dashboard serve            migrate + import + collect, then serve and collect on a timer
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/shyam/dashboard/internal/admin"
	"github.com/shyam/dashboard/internal/backup"
	"github.com/shyam/dashboard/internal/collector"
	"github.com/shyam/dashboard/internal/config"
	"github.com/shyam/dashboard/internal/db"
	"github.com/shyam/dashboard/internal/english"
	"github.com/shyam/dashboard/internal/german"
	"github.com/shyam/dashboard/internal/importer"
	"github.com/shyam/dashboard/internal/llm"
	"github.com/shyam/dashboard/internal/server"
	"github.com/shyam/dashboard/internal/summaries"
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
	// The serve log masks the token, so this is how you read the full address off the Mac.
	if cmd == "url" {
		c, err := config.Load()
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		fmt.Printf("http://%s/d/%s/\n", c.ListenAddr, c.AccessToken)
		fmt.Printf("http://%s/d/%s/api   (set this one in the KOReader plugin)\n", c.ListenAddr, c.AccessToken)
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
	claude := llm.New(cfg.AnthropicKey, cfg.AnthropicModel)
	if cfg.AnthropicURL != "" {
		claude.BaseURL = cfg.AnthropicURL
	}
	col.Course = german.New(pool, claude, cfg.Timezone, cfg.GermanStartDate)
	col.English = english.New(pool, claude, cfg.Timezone)
	col.Summaries = summaries.New(pool, claude, cfg.Timezone)
	if n, err := importer.SeedBookPlan(ctx, pool, cfg.DataDir); err != nil {
		log.Printf("book plan: %v", err)
	} else if n > 0 {
		log.Printf("book plan: loaded %d books from data/books/summaries.yaml", n)
	}

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
	case "notion-inspect":
		if !col.Notion.Configured() {
			log.Fatal("set NOTION_TOKEN and NOTION_TASKS_DB in .env first")
		}
		schema, err := col.Notion.LoadSchema(ctx)
		must(err)
		fmt.Printf("Database: %s\n", schema.Title)
		fmt.Printf("Title property: %s\n", schema.TitleProp)
		fmt.Printf("Status property: %s  options: %v\n", schema.StatusProp, schema.Options)
		fmt.Printf("Optional properties found: due=%q project=%q estimate=%q next step=%q\n", schema.Due, schema.Project, schema.Estimate, schema.NextStep)
		fmt.Println("All properties:")
		for _, n := range sortedKeys(schema.Props) {
			fmt.Printf("  %-24s %s\n", n, schema.Props[n])
		}
		tasks, err := col.Notion.Tasks(ctx)
		must(err)
		fmt.Printf("\n%d tasks visible (hidden status excluded):\n", len(tasks))
		for _, t := range tasks {
			fmt.Printf("  [%-7s] %-14s %s\n", t.Kind, t.Status, t.Title)
		}
	case "german-generate":
		// dashboard german-generate [day N | week N]  (defaults to today's quiz and this week's test)
		if len(os.Args) >= 4 && os.Args[2] == "week" {
			w, err := col.Course.GenerateWritten(ctx, atoiArg(os.Args[3]))
			must(err)
			fmt.Printf("week %d written test: %s (%d reading items)\n%s\n", w.Week, w.Task.Title, len(w.Task.ReadingItems), w.Task.WritingTask)
		} else if len(os.Args) >= 4 && os.Args[2] == "day" {
			q, err := col.Course.GenerateQuiz(ctx, atoiArg(os.Args[3]))
			must(err)
			for i, qq := range q.Questions {
				fmt.Printf("%2d. [%s] %s\n    %v  -> %d\n", i+1, qq.Type, qq.Prompt, qq.Options, qq.Answer)
			}
		} else {
			col.Course.EnsureToday(ctx)
			log.Println("today's quiz and this week's test are ensured")
		}
		_, err := col.Build(ctx)
		must(err)
	case "english-generate":
		// dashboard english-generate [YYYY-MM-DD]  (defaults to today)
		day := col.English.Today()
		if len(os.Args) >= 3 {
			t, err := time.ParseInLocation("2006-01-02", os.Args[2], cfg.Timezone)
			must(err)
			day = t
		}
		p, err := col.English.Generate(ctx, day)
		must(err)
		fmt.Printf("%s: %d phrases, topic %q, passage %q (%d words, ~%d min)\n", p.Day, len(p.Phrases), p.Topic.Title, p.Passage.Title, p.Passage.WordCount, p.Passage.Minutes)
		for _, ph := range p.Phrases {
			fmt.Printf("  - %s (%s): %s\n", ph.Phrase, ph.Register, ph.Meaning)
		}
		_, err = col.Build(ctx)
		must(err)
	case "summary-generate":
		// dashboard summary-generate [N]  (plan day / position; defaults to today's book)
		pos := col.Summaries.DayFor(ctx, col.Summaries.Today())
		if len(os.Args) >= 3 {
			pos = atoiArg(os.Args[2])
		}
		b, err := col.Summaries.BookAt(ctx, pos)
		must(err)
		log.Printf("generating day %d: %s (takes 1 to 3 minutes)", pos, b.Title)
		e, err := col.Summaries.Generate(ctx, b.ID)
		must(err)
		fmt.Printf("%d. %s: %d words (~%d min), %d lessons, %d insights, %d exercises\n", pos, e.Title, e.Summary.WordCount, e.Summary.Minutes,
			len(e.Summary.Lessons), len(e.Summary.Insights), len(e.Summary.Exercises))
		for i, l := range e.Summary.Lessons {
			fmt.Printf("  %2d. %s\n", i+1, l.Title)
		}
		_, err = col.Build(ctx)
		must(err)
	case "backup":
		path, err := backup.Run(ctx, cfg)
		must(err)
		log.Println("backup written:", path)
	case "serve":
		// Postgres is the source of truth. data/ is only loaded when the content tables are empty
		// (first run), so your edits in the web dashboard are never overwritten.
		var n int
		_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM books) + (SELECT count(*) FROM vocab_items) + (SELECT count(*) FROM guitar_plan)`).Scan(&n)
		if n == 0 {
			log.Println("content tables are empty, loading data/ once")
			must(importer.Run(ctx, pool, cfg.DataDir))
		}
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
		if cfg.DailyBackup {
			go backup.Daily(ctx, cfg, log.Printf)
		}
		go func() {
			// generate quizzes and tests on schedule, then rebuild the snapshot so the Kindle sees them
			col.Course.EnsureToday(ctx)
			col.English.EnsureToday(ctx)
			col.Summaries.EnsureToday(ctx)
			_, _ = col.Build(ctx)
			t := time.NewTicker(30 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					col.Course.EnsureToday(ctx)
					col.English.EnsureToday(ctx)
					col.Summaries.EnsureToday(ctx)
					_, _ = col.Build(ctx)
				}
			}
		}()
		srv := server.New(cfg, col)
		adm, err := admin.New(cfg, pool, col)
		must(err)
		srv.Mount(adm.Register)
		must(srv.ListenAndServe(ctx))
	default:
		log.Fatalf("unknown command %q (token|url|migrate|import|seed|collect|backup|notion-inspect|german-generate|english-generate|summary-generate|serve)", cmd)
	}
}

func atoiArg(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
