// Command shop-spike evaluates OCR engines and the receipt parser on real
// receipts (docs/PLAN.md §13, docs/spike.md).
//
//	shop-spike ocr     run engines over spike/receipts, caching results
//	shop-spike label   local web page to label receipts (truth.csv, lines/)
//	shop-spike score   score cached OCR + parser against labels → report
//	shop-spike show    print one receipt's rows and parse
//	shop-spike status  what's imaged, labelled and cached
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"shop/internal/receipt"
	"shop/internal/spike"
)

const usage = `usage: shop-spike <command> [flags]

  status                          images, labels and OCR cache per engine
  ocr   [-engines a,b] [-force]   OCR every image with every engine (cached)
  label [-addr :8090] [-engines a,b]
                                  open a local page to label receipts
  score [-engines a,b] [-out docs/spike-report.md]
                                  score engines against the labels and write the report
  show  <id> [-engine name]       rows + parse for one receipt

common flags: -root <repo root> (default: auto-detected)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	root := fs.String("root", findRoot(), "repo root (contains spike/)")
	engines := fs.String("engines", "", "comma-separated engine names (default: all in spike/engines.json)")
	force := fs.Bool("force", false, "ocr: re-run even if cached")
	addr := fs.String("addr", "127.0.0.1:8090", "label: listen address")
	out := fs.String("out", "", "score: report path (default <root>/docs/spike-report.md)")
	engine := fs.String("engine", "", "show: engine to use (default: first with a cached result)")

	// "show <id> [flags]": pull the id out before flag parsing.
	var showID string
	if cmd == "show" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		showID, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		log.Fatal(err)
	}
	d := spike.Dir{Root: filepath.Join(*root, "spike")}
	all, err := d.LoadEngines()
	if err != nil {
		log.Fatalf("engines: %v", err)
	}
	selected := all
	if *engines != "" {
		want := strings.Split(*engines, ",")
		selected = nil
		for _, e := range all {
			if slices.Contains(want, e.Name) {
				selected = append(selected, e)
			}
		}
		if len(selected) == 0 {
			log.Fatalf("no engines match %q", *engines)
		}
	}
	names := make([]string, len(selected))
	for i, e := range selected {
		names[i] = e.Name
	}

	switch cmd {
	case "status":
		err = status(d, all)
	case "ocr":
		err = spike.RunOCR(ctx, d, *root, selected, *force, os.Stdout)
	case "label":
		err = label(ctx, d, names, loc, *addr)
	case "score":
		path := *out
		if path == "" {
			path = filepath.Join(*root, "docs", "spike-report.md")
		}
		err = score(d, selected, loc, path)
	case "show":
		err = show(d, names, *engine, showID, loc)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// findRoot walks up from the working directory to the folder holding spike/.
func findRoot() string {
	dir, _ := os.Getwd()
	for d := dir; ; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, "spike", "engines.json")); err == nil && !fi.IsDir() {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

func status(d spike.Dir, engines []spike.Engine) error {
	_, ids, err := d.Images()
	if err != nil {
		return err
	}
	truth, err := d.LoadTruth()
	if err != nil {
		return err
	}
	lines := 0
	shops := map[string]int{}
	for _, id := range ids {
		if _, has, _ := d.LoadLines(id); has {
			lines++
		}
		if t, ok := truth[id]; ok {
			shops[t.Shop]++
		}
	}
	fmt.Printf("images: %d   labelled: %d   with lines: %d\n", len(ids), len(truth), lines)
	if len(shops) > 0 {
		fmt.Print("per shop:")
		for s, n := range shops {
			fmt.Printf("  %s=%d", s, n)
		}
		fmt.Println()
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "\nENGINE\tCACHED\tERRORS\tSOURCE")
	for _, e := range engines {
		cached, errs := 0, 0
		for _, id := range ids {
			if c, ok, _ := d.LoadCached(e.Name, id); ok {
				cached++
				if c.Error != "" {
					errs++
				}
			}
		}
		src := e.URL
		if src == "" {
			src = strings.Join(e.Cmd, " ")
		}
		fmt.Fprintf(tw, "%s\t%d/%d\t%d\t%s\n", e.Name, cached, len(ids), errs, src)
	}
	return tw.Flush()
}

func label(ctx context.Context, d spike.Dir, engines []string, loc *time.Location, addr string) error {
	if err := os.MkdirAll(d.Receipts(), 0o755); err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: (&spike.LabelServer{Dir: d, Engines: engines, Loc: loc}).Handler()}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Printf("labelling %s → open http://%s (Ctrl+C to stop)\n", d.Receipts(), ln.Addr())
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func score(d spike.Dir, engines []spike.Engine, loc *time.Location, out string) error {
	_, ids, err := d.Images()
	if err != nil {
		return err
	}
	truth, err := d.LoadTruth()
	if err != nil {
		return err
	}
	if len(truth) == 0 {
		return fmt.Errorf("no labels yet: run `shop-spike label` first")
	}
	lineLabelled := 0
	for id := range truth {
		if _, has, _ := d.LoadLines(id); has {
			lineLabelled++
		}
	}
	var scores []spike.EngineScore
	now := time.Now()
	for _, e := range engines {
		s, err := spike.ScoreEngine(d, e.Name, truth, loc, now)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name, err)
		}
		if s.N == 0 {
			fmt.Printf("%-14s no cached OCR for labelled receipts; skipped\n", e.Name)
			continue
		}
		scores = append(scores, s)
		_, _, f1 := s.LineF1()
		fmt.Printf("%-14s n=%-3d total=%5.1f%%  consistent=%5.1f%%  date=%5.1f%%  auto-ok=%5.1f%%  silent=%d  lineF1=%.2f  median=%dms\n",
			s.Engine, s.N, 100*s.Rate(s.TotalCorrect), 100*s.Rate(s.SelfConsistent), 100*s.Rate(s.DateCorrect),
			100*s.Rate(s.AutoOK), s.SilentErrors, f1, s.MedianMS)
	}
	if len(scores) == 0 {
		return fmt.Errorf("nothing to score: run `shop-spike ocr` first")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := spike.WriteReport(out, scores, engines, len(truth), lineLabelled, len(ids), now); err != nil {
		return err
	}
	if err := spike.WriteResultsCSV(d.ResultsCSV(), scores); err != nil {
		return err
	}
	fmt.Printf("\nreport: %s\nper-receipt results: %s\n", out, d.ResultsCSV())
	return nil
}

func show(d spike.Dir, engines []string, engine, id string, loc *time.Location) error {
	if id == "" {
		return fmt.Errorf("usage: shop-spike show <id> [-engine name]")
	}
	if engine != "" {
		engines = []string{engine}
	}
	for _, e := range engines {
		c, ok, err := d.LoadCached(e, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		fmt.Printf("== %s / %s  (%d ms, %d OCR lines)\n", id, e, c.Result.MS, len(c.Result.Lines))
		if c.Error != "" {
			fmt.Println("OCR error:", c.Error)
			return nil
		}
		p := receipt.Parse(c.Result, receipt.Options{Location: loc, Now: time.Now()})
		fmt.Println("\n-- rows (amount | vat | conf)")
		for _, r := range p.Rows {
			amt := ""
			if r.Amount != nil {
				amt = fmt.Sprintf("%d", *r.Amount)
			}
			fmt.Printf("  %-48s %8s %2s %.2f\n", r.Raw, amt, r.VAT, r.Conf)
		}
		fmt.Println("\n-- parsed lines")
		for i, l := range p.Lines {
			up := ""
			if l.UnitPriceCents != nil {
				up = fmt.Sprintf("@%d", *l.UnitPriceCents)
			}
			parent := ""
			if l.Parent >= 0 {
				parent = fmt.Sprintf(" → line %d", l.Parent+1)
			}
			fmt.Printf("  %2d %-8s %-32s %6.3g %-2s %6s %7d%s\n", i+1, l.Kind, l.RawText, l.Qty, l.Unit, up, l.LineTotalCents, parent)
		}
		total := "none"
		if p.TotalCents != nil {
			total = fmt.Sprintf("%d", *p.TotalCents)
		}
		date := "none"
		if p.PurchasedAt != nil {
			date = p.PurchasedAt.In(loc).Format("2006-01-02 15:04")
		}
		fmt.Printf("\ntotal=%s sum=%d date=%s payment=%s reasons=%v\n", total, p.SumCents, date, p.PaymentMethod, p.Reasons)
		if t, ok := mustTruth(d)[id]; ok {
			fmt.Printf("label: shop=%s date=%s total=%d items=%d\n", t.Shop, t.Date, t.TotalCents, t.Items)
		}
		return nil
	}
	return fmt.Errorf("no cached OCR for %s (engines: %v)", id, engines)
}

func mustTruth(d spike.Dir) map[string]spike.Truth {
	t, _ := d.LoadTruth()
	return t
}
