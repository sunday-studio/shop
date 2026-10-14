package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"shop/internal/autoprod"
	"shop/internal/config"
	"shop/internal/english"
	"shop/internal/jobs"
	"shop/internal/match"
	"shop/internal/products"
	"shop/internal/receipts"
	"shop/internal/shopping"
	"shop/internal/stats"
	"shop/internal/store"
	"shop/internal/tenancy"
	"shop/internal/tenantapp"
	"shop/internal/version"
)

// app holds the wired-up components shared by serve and admin commands. A self-hosted server is the
// single-tenant case of the household seam: one database, one tenancy.Single tenant, one service graph.
type app struct {
	cfg  config.Config
	db   *sql.DB
	prov *tenancy.Single
	rt   *tenantapp.Runtime

	queue    *jobs.Queue
	auto     *autoprod.Service
	receipts *receipts.Service
	products *products.Service
	stats    *stats.Service
	list     *shopping.Service
	matcher  *match.Matcher
	english  *english.Service
}

// close closes the household (its services' background work, then its database).
func (a *app) close() error { return a.prov.Close() }

func openApp(ctx context.Context, log *slog.Logger) (*app, error) {
	return openAppWith(ctx, log, false)
}

// openAppWith opens the database (snapshotting it first when the schema is about to change), migrates it
// and builds the service graph. background starts per-household background work (a server wants it,
// command line tools do not).
func openAppWith(ctx context.Context, log *slog.Logger, background bool) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	conn, err := tenancy.OpenDB(ctx, cfg.DBPath(), cfg.DataDir, log)
	if err != nil {
		return nil, err
	}
	a := &app{cfg: cfg, db: conn, prov: tenancy.NewSingle(conn, cfg.DataDir, cfg.ImagesDir())}
	a.rt = tenantapp.NewRuntime(cfg, log)
	a.rt.Background = background
	t, release, err := a.prov.Acquire(ctx, tenancy.DefaultID)
	if err != nil {
		conn.Close()
		return nil, err
	}
	defer release()
	in, err := a.rt.Instance(ctx, t)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("load glossary: %w", err)
	}
	a.queue, a.auto, a.receipts, a.products = in.Queue, in.Auto, in.Receipts, in.Products
	a.stats, a.list, a.matcher, a.english = in.Stats, in.List, in.Matcher, in.English
	return a, nil
}

func migrate(ctx context.Context, log *slog.Logger) error {
	a, err := openApp(ctx, log)
	if err != nil {
		return err
	}
	defer a.close()
	v, err := store.MigrationVersion(ctx, a.db)
	if err != nil {
		return err
	}
	log.Info("migrated", "schema_version", v)
	return nil
}

func serve(ctx context.Context, log *slog.Logger) error {
	if os.Getenv("SHOP_MODE") == "hosted" {
		log.Warn("SHOP_MODE=hosted is set, but this binary serves one household (single mode). The hosted service is the shop-hosted binary.")
	}
	a, err := openAppWith(ctx, log, true)
	if err != nil {
		return err
	}
	defer a.close()

	srv := &http.Server{
		Addr:              a.cfg.Addr,
		Handler:           a.rt.Gateway(a.prov, tenancy.SingleResolver{}).Handler(),
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
