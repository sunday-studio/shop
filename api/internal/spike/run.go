package spike

import (
	"context"
	"fmt"
	"io"
	"time"
)

// RunOCR sends every image to every engine, skipping cached results unless
// force; cached failures are always retried. Engines run one after another
// and images one at a time, so timings aren't distorted by engines
// competing for CPU.
func RunOCR(ctx context.Context, d Dir, repoRoot string, engines []Engine, force bool, log io.Writer) error {
	paths, ids, err := d.Images()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("no images in %s", d.Receipts())
	}
	for _, e := range engines {
		if err := e.Ready(ctx); err != nil {
			fmt.Fprintf(log, "%-14s SKIP: not reachable (%v)\n", e.Name, err)
			continue
		}
		todo := 0
		for _, id := range ids {
			if needsOCR(d, e.Name, id, force) {
				todo++
			}
		}
		fmt.Fprintf(log, "%-14s %d/%d images to OCR\n", e.Name, todo, len(ids))
		if todo == 0 {
			continue
		}

		mem := startMemSampler(e.Container)
		done, failed := 0, 0
		var version string
		for _, id := range ids {
			if ctx.Err() != nil {
				mem.Stop()
				return ctx.Err()
			}
			if !needsOCR(d, e.Name, id, force) {
				continue
			}
			start := time.Now()
			res, err := e.Recognize(ctx, repoRoot, paths[id])
			c := Cached{Result: res, WallMS: time.Since(start).Milliseconds(), At: time.Now().UTC()}
			if err != nil {
				c.Error = err.Error()
				failed++
				fmt.Fprintf(log, "  %s: %v\n", id, err)
			}
			if res.Version != "" {
				version = res.Version
			}
			if err := d.SaveCached(e.Name, id, c); err != nil {
				mem.Stop()
				return err
			}
			done++
			if done%10 == 0 || done == todo {
				fmt.Fprintf(log, "  %d/%d (last %s: %d ms)\n", done, todo, id, c.WallMS)
			}
		}
		meta := d.LoadMeta(e.Name)
		if peak := mem.Stop(); peak > meta.PeakMemMB || force {
			meta.PeakMemMB = peak
		}
		if version != "" {
			meta.Version = version
		}
		meta.Updated = time.Now().UTC()
		if err := d.SaveMeta(e.Name, meta); err != nil {
			return err
		}
		if failed > 0 {
			fmt.Fprintf(log, "  %d failures (re-run to retry them)\n", failed)
		}
	}
	return nil
}

func needsOCR(d Dir, engine, id string, force bool) bool {
	c, ok, err := d.LoadCached(engine, id)
	return force || !ok || err != nil || c.Error != ""
}
