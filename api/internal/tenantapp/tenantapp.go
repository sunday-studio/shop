// Package tenantapp builds the service graph of one household and wires the process-level pieces
// (OCR clients, Open Food Facts client, the fair job scheduler) to it.
//
// Everything a household owns is created here, from the household's own database handle, and hangs on
// the tenancy.Tenant: when the tenant is closed or evicted its services go with it. Nothing built here
// is shared between households except stateless clients of outside services.
//
// Single mode (a self-hosted server) is a Runtime with one tenancy.Single tenant, so the code that
// serves one household and the code that serves many is the same code.
package tenantapp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"shop/internal/autoprod"
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
	"shop/internal/shopsuggest"
	"shop/internal/stats"
	"shop/internal/tenancy"
)

// Runtime holds what every household shares: configuration and clients of outside services. None of it
// holds household data.
type Runtime struct {
	Cfg         config.Config
	Log         *slog.Logger
	OCR         *ocr.Client
	OCRFallback *ocr.Client // nil when not configured
	FoodFacts   *openfoodfacts.Client

	// Background starts per-household background work when a household is built (reading receipt
	// headers that have none yet). A server turns it on; command line tools leave it off.
	Background bool

	// Test hooks.
	LogoBaseURL string                            // replaces the logo.dev URL
	Backoff     func(attempt int64) time.Duration // replaces the job retry backoff

	fair *jobs.Fair
}

// NewRuntime creates the shared clients from the configuration.
func NewRuntime(cfg config.Config, log *slog.Logger) *Runtime {
	rt := &Runtime{Cfg: cfg, Log: log, OCR: ocr.New(cfg.OCRURL)}
	if cfg.OCRFallbackURL != "" {
		rt.OCRFallback = ocr.New(cfg.OCRFallbackURL)
	}
	ff := openfoodfacts.New(cfg.FoodFactsUserAgent)
	if cfg.FoodFactsBaseURL != "" {
		ff = ff.WithBaseURL(cfg.FoodFactsBaseURL)
	}
	rt.FoodFacts = ff
	return rt
}

// UseScheduler makes the runtime tell the fair scheduler which household has work.
func (rt *Runtime) UseScheduler(f *jobs.Fair) { rt.fair = f }

// Instance is one household's service graph.
type Instance struct {
	Tenant   *tenancy.Tenant
	Queue    *jobs.Queue
	Receipts *receipts.Service
	Products *products.Service
	Auto     *autoprod.Service
	Stats    *stats.Service
	List     *shopping.Service
	Matcher  *match.Matcher
	English  *english.Service
	Search   *search.Service
	Server   *httpapi.Server
	MCP      *mcp.Server

	handler http.Handler
}

// Handler is the household's API (its Server's routes, with /mcp mounted behind its token check).
func (i *Instance) Handler() http.Handler { return i.handler }

type instanceKey struct{}

// Instance returns the household's service graph, building it on first use. Building loads the
// household's glossary and brings automatic English names up to date.
func (rt *Runtime) Instance(ctx context.Context, t *tenancy.Tenant) (*Instance, error) {
	v, err := t.State(instanceKey{}, func() (any, error) { return rt.build(ctx, t) })
	if err != nil {
		return nil, err
	}
	return v.(*Instance), nil
}

func (rt *Runtime) build(ctx context.Context, t *tenancy.Tenant) (*Instance, error) {
	log := rt.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	cfg := rt.Cfg
	in := &Instance{Tenant: t}
	conn := t.DB
	in.Queue = jobs.New(conn, log)
	if rt.Backoff != nil {
		in.Queue.Backoff = rt.Backoff
	}
	if rt.fair != nil {
		id, f := t.ID, rt.fair
		in.Queue.SetNotifyHook(func() { f.Hint(id) })
	}
	in.Receipts = receipts.New(conn, rt.OCR, rt.OCRFallback, in.Queue,
		receipts.Config{ImagesDir: t.ImagesDir, Location: cfg.Location}, log)
	in.Matcher = match.NewMatcher(conn)
	in.Products = products.New(conn, in.Matcher, rt.FoodFacts, t.ImagesDir, log)
	in.Auto = autoprod.New(conn, in.Matcher, in.Products, rt.FoodFacts, in.Queue, log, cfg.AutoProducts)
	in.Auto.RegisterJobs(in.Queue)
	// Install the household's glossary, then bring automatic English names up to date (new
	// products, a grown built-in glossary).
	in.English = english.New(conn)
	if err := in.English.Load(ctx); err != nil {
		return nil, err
	}
	in.Products.UseGlossary(in.English.Glossary())
	in.Auto.UseGlossary(in.English.Glossary())
	if n, err := in.English.Retranslate(ctx); err != nil {
		log.Warn("english names", "household", t.ID, "err", err)
	} else if n > 0 {
		log.Info("english names updated", "household", t.ID, "products", n)
	}
	in.Stats = stats.New(conn, cfg.Location)
	in.List = shopping.New(conn, in.Matcher, in.Stats).WithLocation(cfg.Location)
	in.Search = search.New(conn, in.Products, in.Matcher)
	lg := logos.New(cfg.LogoDevToken, t.ImagesDir)
	if rt.LogoBaseURL != "" {
		lg = lg.WithBaseURL(rt.LogoBaseURL)
	}

	in.MCP = mcpserver.New(mcpserver.Deps{
		DB: conn, Receipts: in.Receipts, Products: in.Products, Stats: in.Stats, List: in.List,
		Matcher: in.Matcher, English: in.English, Location: cfg.Location, Auto: in.Auto,
	})
	in.Server = httpapi.New(httpapi.Deps{
		DB: conn, OCR: rt.OCR, OCRFallback: rt.OCRFallback, Receipts: in.Receipts,
		Products: in.Products, Stats: in.Stats, List: in.List, English: in.English, Auto: in.Auto,
		Search: in.Search, Logos: lg, Location: cfg.Location, Log: log, DataDir: t.Dir,
		Authed: map[string]http.Handler{"/mcp": mcpserver.Handler(in.MCP)},
	})
	in.handler = in.Server.Inner()

	if rt.Background {
		// Read the shop-suggestion header of receipts that have none yet (first start after an upgrade).
		bg, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			if n, err := shopsuggest.NewService(conn).Reindex(bg); err != nil {
				if bg.Err() == nil {
					log.Warn("reindex receipt headers", "household", t.ID, "err", err)
				}
			} else if n > 0 {
				log.Info("receipt headers indexed", "household", t.ID, "receipts", n)
			}
		}()
		t.OnClose(func() { cancel(); <-done })
	}
	return in, nil
}

// Gateway returns the HTTP gateway for a provider: every request resolves its household and is
// answered by that household's service graph.
func (rt *Runtime) Gateway(p tenancy.Provider, r tenancy.Resolver) *httpapi.Gateway {
	return &httpapi.Gateway{
		Provider: p, Resolver: r, OCR: rt.OCR, OCRFallback: rt.OCRFallback, Log: rt.Log,
		Tenant: func(ctx context.Context, t *tenancy.Tenant) (http.Handler, error) {
			in, err := rt.Instance(ctx, t)
			if err != nil {
				return nil, err
			}
			return in.Handler(), nil
		},
	}
}

// queues adapts a provider to the fair scheduler.
type queues struct {
	rt *Runtime
	p  tenancy.Provider
}

func (q queues) IDs(ctx context.Context) ([]string, error) { return q.p.IDs(ctx) }

func (q queues) Queue(ctx context.Context, id string) (*jobs.Queue, func(), error) {
	t, release, err := q.p.Acquire(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	in, err := q.rt.Instance(ctx, t)
	if err != nil {
		release()
		return nil, nil, err
	}
	return in.Queue, release, nil
}

// Scheduler returns a fair scheduler over all households of the provider (one reading job at a time
// per household, households in turn) and wires the runtime to it. Run it with Run.
func (rt *Runtime) Scheduler(p tenancy.Provider, workers int) *jobs.Fair {
	f := &jobs.Fair{Source: queues{rt: rt, p: p}, Workers: workers, Log: rt.Log}
	rt.UseScheduler(f)
	return f
}
