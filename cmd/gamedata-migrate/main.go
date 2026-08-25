// Command gamedata-migrate moves Project Sekai suite/mysekai documents from
// MongoDB into the PostgreSQL wide tables.
//
//	gamedata-migrate estimate                 # count and size, writes nothing
//	gamedata-migrate migrate                  # DRY RUN unless -apply is given
//	gamedata-migrate migrate -apply           # the real backfill
//	gamedata-migrate verify                   # re-read both sides and compare
//
// It is deleted once MongoDB is decommissioned; anything it needs is built here
// rather than added to the server's dependency graph.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "estimate":
		err = runEstimate(ctx, os.Args[2:])
	case "migrate":
		err = runMigrate(ctx, os.Args[2:])
	case "verify":
		err = runVerify(ctx, os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gamedata-migrate <estimate|migrate|verify> [flags]

  estimate   report document counts and sizes; writes nothing
  migrate    backfill MongoDB -> PostgreSQL (DRY RUN unless -apply)
  verify     re-read both stores and compare, key by key

Run a subcommand with -h for its flags.
`)
}

// commonFlags are shared by every subcommand.
type commonFlags struct {
	mongoURI   string
	mongoDB    string
	suiteColl  string
	mysekaiCol string
	pgURL      string
	dataType   string
	server     string
	limit      int64
	progress   int
	verbose    bool
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.mongoURI, "mongo-uri", envOr("MONGODB_URL", "mongodb://127.0.0.1:27017"), "MongoDB URI")
	fs.StringVar(&c.mongoDB, "mongo-db", envOr("MONGODB_DB", "collections"), "MongoDB database")
	fs.StringVar(&c.suiteColl, "suite-collection", envOr("MONGODB_SUITE_COLLECTION", "suite"), "suite collection")
	fs.StringVar(&c.mysekaiCol, "mysekai-collection", envOr("MONGODB_MYSEKAI_COLLECTION", "mysekai"), "mysekai collection")
	fs.StringVar(&c.pgURL, "pg", envOr("GAME_DATA_URL", ""), "PostgreSQL DSN for the game-data tables")
	fs.StringVar(&c.dataType, "data-type", "all", "suite | mysekai | all")
	fs.StringVar(&c.server, "server", "", "limit to one region (jp/en/tw/kr/cn)")
	fs.Int64Var(&c.limit, "limit", 0, "process at most N documents per collection (0 = all)")
	fs.IntVar(&c.progress, "progress", 500, "print progress every N documents (0 = silent)")
	fs.BoolVar(&c.verbose, "v", false, "log every document")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// collections returns the (collection, catalogName) pairs the flags select.
func (c *commonFlags) collections() ([][2]string, error) {
	switch c.dataType {
	case "suite":
		return [][2]string{{c.suiteColl, "suite"}}, nil
	case "mysekai":
		return [][2]string{{c.mysekaiCol, "mysekai"}}, nil
	case "all":
		return [][2]string{{c.suiteColl, "suite"}, {c.mysekaiCol, "mysekai"}}, nil
	}
	return nil, fmt.Errorf("-data-type must be suite, mysekai or all, got %q", c.dataType)
}
