package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"pantry/internal/config"
	"pantry/internal/httpapi"
	"pantry/internal/jobs"
	"pantry/internal/match"
	"pantry/internal/ocr"
	"pantry/internal/openfoodfacts"
	"pantry/internal/products"
	"pantry/internal/receipts"
	"pantry/internal/store"
	"pantry/internal/version"
)

// app holds the wired-up components shared by serve and admin commands.
type app struct {
	cfg      config.Config
	db       *sql.DB
	queue    *jobs.Queue
	ocr      *ocr.Client
	fallback *ocr.Client
	receipts *receipts.Service
	products *products.Service
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
	a.products = products.New(conn, match.NewMatcher(conn), openfoodfacts.New(cfg.FoodFactsUserAgent), cfg.ImagesDir(), log)
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

	api := httpapi.New(httpapi.Deps{
		DB: a.db, OCR: a.ocr, OCRFallback: a.fallback, Receipts: a.receipts,
		Products: a.products, Location: a.cfg.Location, Log: log,
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
