// Command shop is the Shop backend: API server and admin CLI.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"shop/internal/version"
)

const usage = `usage: shop <command> [flags]

server:
  serve                         run the API server + job worker (applies migrations)
  migrate                       apply database migrations and exit

admin (run where the database lives, e.g. docker compose exec api shop ...):
  password generate             create a random pairing password (printed once) for the app
  password set                  choose the pairing password (prompted, or one line on stdin)
  password status               is pairing set up; paired devices
                                (add -revoke-devices to generate/set to unpair every device)
  token create <name>           create an API token (printed once), e.g. for the assistant
  token list                    list tokens
  token revoke <name>           delete a token
  reparse [-status S]           re-parse unedited receipts from stored OCR output
  backup [-dir D] [-schedule]   snapshot DB + mirror images to D; -schedule runs daily
  restore -yes [-dir D] [-snapshot NAME|latest]
                                replace the DB with a snapshot (API must be stopped)
  healthcheck                   exit 0 if the local server answers (container probe)

client (talks to a running server over HTTP):
  import -shop ID [-url U] [-token T] <dir|file>...
                                upload receipt images (JPEG/PNG)

  version                       print version
`

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(ctx, log)
	case "migrate":
		err = migrate(ctx, log)
	case "password":
		err = passwordCmd(ctx, args)
	case "token":
		err = tokenCmd(ctx, args)
	case "reparse":
		err = reparseCmd(ctx, args)
	case "backup":
		err = backupCmd(ctx, log, args)
	case "restore":
		err = restoreCmd(ctx, args)
	case "healthcheck":
		err = healthcheckCmd()
	case "import":
		err = importCmd(ctx, args)
	case "version":
		fmt.Printf("shop %s (%s)\n", version.Version, version.Commit)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
