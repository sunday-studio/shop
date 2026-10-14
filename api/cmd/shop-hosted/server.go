package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"shop/internal/hosted/admin"
	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedapi"
	"shop/internal/hosted/lifecycle"
	"shop/internal/hosted/limits"
	"shop/internal/tenancy"
	"shop/internal/tenantapp"
	"shop/internal/version"
)

// hostedServer is the assembled hosted service.
type hostedServer struct {
	cfg   hostedConfig
	log   *slog.Logger
	Store *control.Store
	Ctl   *control.Service
	Calc  *billing.Calculator
	Bill  *billing.Service
	Auth  *appleauth.Service
	Gr    *grants.Service
	Meter *limits.Meter
	Prov  *tenancy.Multi
	RT    *tenantapp.Runtime
	API   *hostedapi.API
	Life  *lifecycle.Runner
	Sched *hostedapi.Scheduler
	Apple *appleParts
}

// devSealerKey is used only with SHOP_APPLE_MODE=fake when no key is configured.
var devSealerKey = []byte("shop-hosted-dev-sealer-key-32by!")

func newServer(cfg hostedConfig, log *slog.Logger, clock hclock.Clock, background bool) (*hostedServer, error) {
	s := &hostedServer{cfg: cfg, log: log}
	apple, err := buildApple(cfg, clock, log)
	if err != nil {
		return nil, err
	}
	if off, ok := clock.(*hclock.Offset); ok && apple.Dev != nil {
		apple.Dev = devWithClock(apple.Dev, off) // fake Apple only: POST /admin/v1/dev/advance-clock
	}
	s.Apple = apple
	key := cfg.SealerKey
	if key == nil {
		if cfg.Apple.Mode != "fake" {
			return nil, errors.New("SHOP_SEALER_KEY is required (32 bytes, 64 hex characters or base64): it encrypts the stored Apple refresh tokens")
		}
		key = devSealerKey
	}
	sealer, err := appleauth.NewAESGCMSealer(key)
	if err != nil {
		return nil, err
	}
	if s.Store, err = control.Open(cfg.ControlDB, clock); err != nil {
		return nil, err
	}
	s.Calc = billing.NewCalculator(s.Store)
	s.Ctl = control.NewService(s.Store, cfg.API.Limits.ControlConfig(control.DefaultConfig()), s.Calc.AccessChecker())
	s.Bill = billing.NewService(s.Ctl, apple.Verifier, apple.API, billing.Config{BundleID: cfg.Apple.BundleID, ProductIDs: cfg.Apple.ProductIDs, AllowLocal: cfg.Apple.Mode == "fake", ProductionOnly: cfg.Apple.Environment == "production"})
	s.Bill.Log = log
	s.Auth = appleauth.New(s.Ctl, &appleauth.Verifier{Keys: &appleauth.KeySet{Fetcher: apple.Keys, Clock: clock}, Clock: clock, Audience: cfg.Apple.BundleID}, apple.Exchanger, apple.Revoker, sealer)
	s.Auth.Log = log
	s.Gr = grants.New(s.Ctl, grants.DefaultConfig())
	s.Meter = limits.NewMeter(s.Store, cfg.API.Limits)

	s.RT = tenantapp.NewRuntime(cfg.App, log)
	s.RT.Background = background
	if s.Prov, err = tenancy.NewMulti(tenancy.MultiConfig{Root: cfg.App.DataDir, BackupRoot: cfg.BackupDir, MaxOpen: 64, Log: log}); err != nil {
		return nil, err
	}
	if s.API, err = hostedapi.New(hostedapi.Deps{Cfg: cfg.API, Clock: clock, Log: log, Ctl: s.Ctl, Calc: s.Calc, Bill: s.Bill, Auth: s.Auth, Grants: s.Gr,
		Meter: s.Meter, Gate: limits.NewGate(256), Provider: s.Prov, Runtime: s.RT, Dev: apple.Dev}); err != nil {
		return nil, err
	}
	layout := lifecycle.Layout{DataDir: cfg.App.DataDir, BackupDir: cfg.BackupDir, ExportDir: cfg.ExportDir}
	lcfg := lifecycle.DefaultConfig()
	lcfg.KillSwitch = cfg.KillSwitch
	sinks := hostedapi.MultiSink{hostedapi.LogSink{Log: log}}
	if cfg.WebhookURL != "" {
		sinks = append(sinks, hostedapi.WebhookSink{URL: cfg.WebhookURL, Secret: cfg.WebhookToken})
	}
	if cfg.SMTPAddr != "" {
		sinks = append(sinks, hostedapi.SMTPSink{Addr: cfg.SMTPAddr, From: cfg.SMTPFrom, User: cfg.SMTPUser, Pass: cfg.SMTPPass, Support: cfg.API.SupportEmail})
	}
	s.Life = &lifecycle.Runner{S: s.Store, Ctl: s.Ctl, Calc: s.Calc, Apple: s.Bill, Revoker: s.Auth,
		Eraser: hostedapi.Eraser{Provider: s.Prov, OS: lifecycle.OSEraser{Layout: layout}}, Layout: layout, Sink: sinks, Cfg: lcfg, Log: log}
	s.Sched = &hostedapi.Scheduler{API: s.API, Runner: s.Life, Interval: cfg.Interval, Log: log}
	return s, nil
}

func (s *hostedServer) close() {
	_ = s.Prov.Close()
	_ = s.Store.Close()
}

// newHTTPServer is a listener with the timeouts a public server needs: a client that trickles its
// headers or body (or keeps an idle connection) must not hold a goroutine and a descriptor forever.
// There is no WriteTimeout: export downloads stream for as long as the client's link is slow.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 5 * time.Minute,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
}

func serve(ctx context.Context, log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.App.DataDir, 0o755); err != nil {
		return err
	}
	clock, err := serveClock(cfg)
	if err != nil {
		return err
	}
	s, err := newServer(cfg, log, clock, true)
	if err != nil {
		return err
	}
	defer s.close()
	if cfg.AdminHash == "" {
		log.Warn("ADMIN_TOKEN_HASH is not set: the admin API refuses every request (create a token with `shop-hosted admin token create`)")
	}

	bg, stop := context.WithCancel(ctx)
	defer stop()
	fair := s.RT.Scheduler(&hostedapi.SchedulerView{Provider: s.Prov, API: s.API}, cfg.App.Workers)
	done := make(chan struct{}, 3)
	go func() { fair.Run(bg); done <- struct{}{} }()
	go func() { s.Sched.Run(bg); done <- struct{}{} }()
	s.API.StartExports(bg)

	pub := newHTTPServer(cfg.Addr, s.API.Handler())
	adm := newHTTPServer(cfg.AdminAddr, s.API.AdminHandler(cfg.AdminHash))
	errc := make(chan error, 2)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "mode", "hosted", "version", version.Version, "apple", cfg.Apple.Mode, "environment", cfg.Apple.Environment)
		errc <- pub.ListenAndServe()
	}()
	go func() {
		log.Info("admin listening", "addr", cfg.AdminAddr)
		errc <- adm.ListenAndServe()
	}()
	var runErr error
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pub.Shutdown(sctx); err != nil && runErr == nil {
		runErr = err
	}
	_ = adm.Shutdown(sctx)
	stop()
	for i := 0; i < 2; i++ {
		<-done
	}
	return runErr
}

// adminCmd runs `shop-hosted admin ...`.
func adminCmd(ctx context.Context, args []string) int {
	if len(args) >= 2 && args[0] == "token" && args[1] == "create" {
		tok, hash := hostedapi.NewAdminToken()
		fmt.Fprintln(os.Stderr, "Admin token (shown once). Keep it secret; put the hash in the server's ADMIN_TOKEN_HASH.")
		fmt.Println(tok)
		fmt.Fprintf(os.Stderr, "ADMIN_TOKEN_HASH=%s\n", hash)
		return 0
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s, err := newServer(cfg, quiet, hclock.System{}, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer s.close()
	env := admin.Env{Ctl: s.Ctl, Grants: s.Gr, Calc: s.Calc, Lifecycle: s.Life, Operator: os.Getenv("SHOP_OPERATOR"), Out: os.Stdout, Err: os.Stderr}
	// A real lifecycle run waits for the server's scheduler (one run at a time, across processes).
	if len(args) >= 2 && args[0] == "lifecycle" && args[1] == "run" && !contains(args, "--dry-run") && !contains(args, "-dry-run") {
		holder := "cli-" + control.NewUUID()
		ok, err := s.Store.TryLock(ctx, "scheduler", holder, 30*time.Minute)
		if err != nil || !ok {
			fmt.Fprintln(os.Stderr, "error: another lifecycle run is in progress; try again in a minute")
			return 1
		}
		defer s.Store.Unlock(context.WithoutCancel(ctx), "scheduler", holder)
	}
	return admin.Run(ctx, args, env)
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
