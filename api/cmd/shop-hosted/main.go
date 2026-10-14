// Command shop-hosted is the hosted (multi-household, paid) Shop server: SHOP_MODE=hosted.
//
// It is a separate binary in the same Go module as cmd/shop. The single-household server keeps its
// own binary and stays unchanged; this one wires the tenant provider on the control database, the
// Apple clients, the plan gate and limits, the admin listener and the scheduler. See
// notes/hosted-api-contract.md and docs/BACKEND-STATUS.md.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shop/internal/hosted/hclock"
	"shop/internal/version"
)

const usage = `usage: shop-hosted <command>

  serve                        run the hosted server (public listener, admin listener, scheduler)
  migrate                      open (and migrate) the control database, then exit
  admin <command>              operator commands (talk to the control database directly):
      token create                       make an admin token for the HTTP admin API (printed once)
      grant create|list|extend|revoke    complimentary access
      account find                       --support-code K7PQ-4MXD | --account-id ID | --email a@b
      audit tail
      lifecycle run [--dry-run]
    mutating commands need --operator NAME or SHOP_OPERATOR
  healthcheck                  exit 0 if the local server answers
  version

Configuration is read from the environment (see cmd/shop-hosted/config.go): SHOP_MODE=hosted,
SHOP_DATA_DIR, SHOP_CONTROL_DB, ADMIN_ADDR, ADMIN_TOKEN_HASH, SHOP_APPLE_*, ...
`

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(ctx, log)
	case "migrate":
		err = migrateCmd()
	case "admin":
		stop()
		os.Exit(adminCmd(context.Background(), os.Args[2:]))
	case "healthcheck":
		err = healthcheck()
	case "version":
		fmt.Printf("shop-hosted %s (%s)\n", version.Version, version.Commit)
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

func migrateCmd() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.App.DataDir, 0o755); err != nil {
		return err
	}
	s, err := newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), hclock.System{}, false)
	if err != nil {
		return err
	}
	s.close()
	fmt.Println("control database is up to date")
	return nil
}

func healthcheck() error {
	addr := env("SHOP_ADDR", ":8080")
	if len(addr) > 0 && addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/v1/server-info")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("server-info answered %d", resp.StatusCode)
	}
	return nil
}
