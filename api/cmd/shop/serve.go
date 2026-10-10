package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"shop/internal/autoprod"
	"time"

	"shop/internal/backup"
	"shop/internal/config"
	"shop/internal/english"
	"shop/internal/httpapi"
	"shop/internal/jobs"
	"shop/internal/logos"
	"shop/internal/match"
	"shop/internal/mcpserver"
	"shop/internal/ocr"
	"shop/internal/openfoodfacts"
	"shop/internal/products"
	"shop/internal/receipts"
	"shop/internal/search"
	"shop/internal/shopping"
	"shop/internal/stats"
	"shop/internal/store"
	"shop/internal/version"
)

// app holds the wired-up components shared by serve and admin commands.
type app struct {
	cfg      config.Config
	db       *sql.DB
	queue    *jobs.Queue
	auto     *autoprod.Service
	ocr      *ocr.Client
	fallback *ocr.Client
	receipts *receipts.Service
	products *products.Service
	stats    *stats.Service
	list     *shopping.Service
	matcher  *match.Matcher
	english  *english.Service
}

func openApp(ctx context.Context, log *slog.Logger) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	conn, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	// Safety net for upgrades: snapshot an existing database before its
	// schema changes. Snapshots older than the newest few are pruned.
	if pending, v, err := store.PendingMigrations(ctx, conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("check migrations: %w", err)
	} else if pending && v > 0 {
		path := filepath.Join(cfg.DataDir, fmt.Sprintf("pre-migrate-v%d-%s.db", v, time.Now().UTC().Format("20060102-150405")))
		if err := backup.Snapshot(ctx, conn, path); err != nil {
			conn.Close()
			return nil, fmt.Errorf("pre-migration snapshot: %w", err)
		}
		log.Info("pre-migration snapshot", "path", path, "from_version", v)
	}
	if err := store.Migrate(ctx, conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	a := &app{cfg: cfg, db: conn, queue: jobs.New(conn, log), ocr: ocr.New(cfg.OCRURL)}
	if cfg.OCRFallbackURL != "" {
		a.fallback = ocr.New(cfg.OCRFallbackURL)
	}
	a.receipts = receipts.New(conn, a.ocr, a.fallback, a.queue,
		receipts.Config{ImagesDir: cfg.ImagesDir(), Location: cfg.Location}, log)
	a.matcher = match.NewMatcher(conn)
	foodFacts := openfoodfacts.New(cfg.FoodFactsUserAgent)
	a.products = products.New(conn, a.matcher, foodFacts, cfg.ImagesDir(), log)
	a.auto = autoprod.New(conn, a.matcher, a.products, foodFacts, a.queue, log, cfg.AutoProducts)
	a.auto.RegisterJobs(a.queue)
	// Install the user's glossary, then bring automatic English names up to
	// date (new products, a grown built-in glossary).
	a.english = english.New(conn)
	if err := a.english.Load(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("load glossary: %w", err)
	}
	if n, err := a.english.Retranslate(ctx); err != nil {
		log.Warn("english names", "err", err)
	} else if n > 0 {
		log.Info("english names updated", "products", n)
	}
	a.stats = stats.New(conn, cfg.Location)
	a.list = shopping.New(conn, a.matcher, a.stats)
	return a, nil
}

func migrate(ctx context.Context, log *slog.Logger) error {
	a, err := openApp(ctx, log)
	if err != nil {
		return err
	}
	defer a.db.Close()
	v, err := store.MigrationVersion(ctx, a.db)
	if err != nil {
		return err
	}
	log.Info("migrated", "schema_version", v)
	return nil
}

func serve(ctx context.Context, log *slog.Logger) error {
	a, err := openApp(ctx, log)
	if err != nil {
		return err
	}
	defer a.db.Close()

	mcpSrv := mcpserver.New(mcpserver.Deps{
		DB: a.db, Receipts: a.receipts, Products: a.products, Stats: a.stats, List: a.list,
		Matcher: a.matcher, English: a.english, Location: a.cfg.Location, Auto: a.auto,
	})
	api := httpapi.New(httpapi.Deps{
		DB: a.db, OCR: a.ocr, OCRFallback: a.fallback, Receipts: a.receipts,
		Products: a.products, Stats: a.stats, List: a.list, English: a.english, Auto: a.auto,
		Search: search.New(a.db, a.products, a.matcher), Logos: logos.New(a.cfg.LogoDevToken, a.cfg.ImagesDir()),
		Location: a.cfg.Location, Log: log,
		DataDir: a.cfg.DataDir,
		Authed:  map[string]http.Handler{"/mcp": mcpserver.Handler(mcpSrv)},
	})

	srv := &http.Server{
		Addr:              a.cfg.Addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	workerCtx, stopWorkers := context.WithCancel(ctx)
	workersDone := make(chan struct{})
	go func() {
		a.queue.Run(workerCtx, a.cfg.Workers)
		close(workersDone)
	}()

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", a.cfg.Addr, "version", version.Version, "data_dir", a.cfg.DataDir)
		errc <- srv.ListenAndServe()
	}()

	var runErr error
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		runErr = srv.Shutdown(shutdownCtx)
	}
	stopWorkers()
	<-workersDone
	return runErr
}
