package hostedapi

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"shop/internal/hosted/control"
	"shop/internal/store"
)

type exportOut struct {
	ID          string         `json:"id"`
	Status      string         `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   *time.Time     `json:"started_at"`
	FinishedAt  *time.Time     `json:"finished_at"`
	ExpiresAt   *time.Time     `json:"expires_at"`
	SizeBytes   *int64         `json:"size_bytes"`
	DownloadURL *string        `json:"download_url"`
	Error       map[string]any `json:"error"`
}

func (a *API) exportView(j control.ExportJob) exportOut {
	status := j.Status
	if status == control.ExportReady && !j.ExpiresAt.IsZero() && !a.Store.Now().Before(j.ExpiresAt) {
		status = control.ExportExpired
	}
	out := exportOut{ID: j.ID, Status: status, CreatedAt: j.CreatedAt, StartedAt: tptr(j.StartedAt)}
	if status == control.ExportReady || status == control.ExportExpired || status == control.ExportFailed {
		out.FinishedAt = tptr(j.FinishedAt)
	}
	if status == control.ExportReady || status == control.ExportExpired {
		out.ExpiresAt = tptr(j.ExpiresAt)
	}
	if status == control.ExportReady {
		sz := j.SizeBytes
		u := "/v1/account/export/" + j.ID + "/download"
		out.SizeBytes, out.DownloadURL = &sz, &u
	}
	if status == control.ExportFailed {
		out.Error = map[string]any{"code": "export_failed", "message": j.Error}
	}
	return out
}

// exportContext: the caller needs a household; any member may export in every state.
func (a *API) exportContext(w http.ResponseWriter, r *http.Request, p *principal) bool {
	if p.HH.ID == "" || p.HH.Status == control.HouseholdDeleted {
		a.fail(w, r, control.ErrNoHousehold)
		return false
	}
	return true
}

func (a *API) createExport(w http.ResponseWriter, r *http.Request, p *principal) {
	if !a.exportContext(w, r, p) {
		return
	}
	ctx := r.Context()
	if j, err := a.Store.LatestExportJobBy(ctx, p.HH.ID, p.Acc.ID); err == nil && (j.Status == control.ExportQueued || j.Status == control.ExportRunning) {
		writeJSON(w, http.StatusOK, a.exportView(j))
		return
	}
	if err := a.Meter.StartExport(ctx, p.HH.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	j, err := a.Store.InsertExportJob(ctx, p.HH.ID, p.Acc.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.exports.kick()
	writeJSON(w, http.StatusAccepted, a.exportView(j))
}

func (a *API) getExport(w http.ResponseWriter, r *http.Request, p *principal) {
	if !a.exportContext(w, r, p) {
		return
	}
	j, err := a.Store.LatestExportJobBy(r.Context(), p.HH.ID, p.Acc.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.exportView(j))
}

func (a *API) downloadExport(w http.ResponseWriter, r *http.Request, p *principal) {
	if !a.exportContext(w, r, p) {
		return
	}
	j, err := a.Store.ExportJobByID(r.Context(), r.PathValue("id"))
	// An export holds the requester's own account data (email, support code): only they may download it.
	if err != nil || j.HouseholdID != p.HH.ID || j.RequestedBy != p.Acc.ID {
		a.fail(w, r, control.ErrNotFound)
		return
	}
	v := a.exportView(j)
	switch v.Status {
	case control.ExportExpired:
		a.fail(w, r, newErr(http.StatusGone, "export_expired", "This export has expired. Make a new one."))
		return
	case control.ExportReady:
	default:
		a.fail(w, r, newErr(http.StatusConflict, "export_not_ready", "The export is not ready yet."))
		return
	}
	f, err := os.Open(j.Path)
	if err != nil {
		a.fail(w, r, newErr(http.StatusGone, "export_expired", "This export is no longer available. Make a new one."))
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="shop-export-%s.zip"`, j.FinishedAt.UTC().Format("2006-01-02")))
	http.ServeContent(w, r, "export.zip", j.FinishedAt, f)
}

// ---- the worker ----

type exportRunner struct {
	a    *API
	wake chan struct{}
	mu   sync.Mutex
}

func newExportRunner(a *API) *exportRunner { return &exportRunner{a: a, wake: make(chan struct{}, 1)} }

func (e *exportRunner) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// StartExports runs the export worker until ctx ends. Jobs interrupted by a restart run again.
func (a *API) StartExports(ctx context.Context) {
	if n, err := a.Store.RequeueRunningExports(ctx); err != nil {
		a.Log.Error("requeue exports", "err", err)
	} else if n > 0 {
		a.Log.Info("exports requeued after restart", "count", n)
	}
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			a.exports.drain(ctx)
			select {
			case <-ctx.Done():
				return
			case <-a.exports.wake:
			case <-t.C:
			}
		}
	}()
	a.exports.kick()
}

// RunExports works through the queue once, synchronously (tests and the scheduler).
func (a *API) RunExports(ctx context.Context) { a.exports.drain(ctx) }

func (e *exportRunner) drain(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a := e.a
	jobs, err := a.Store.ExportJobsByStatus(ctx, control.ExportQueued)
	if err != nil {
		a.Log.Error("list export jobs", "err", err)
		return
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		if ok, err := a.Store.ClaimExportJob(ctx, j.ID); err != nil || !ok {
			continue
		}
		path, size, err := a.buildExport(ctx, j)
		if err != nil {
			a.Log.Error("export failed", "job", j.ID, "err", err)
			_ = a.Store.FailExportJob(context.WithoutCancel(ctx), j.ID, "Could not read the household data.")
			continue
		}
		if err := a.Store.FinishExportJob(ctx, j.ID, path, size, a.Store.Now().Add(a.Cfg.ExportTTL)); err != nil {
			a.Log.Error("finish export", "job", j.ID, "err", err)
		}
	}
}

// CleanupExports expires finished exports past their time and removes old failed or stale files.
func (a *API) CleanupExports(ctx context.Context) (int, error) {
	jobs, err := a.Store.ExportJobsByStatus(ctx, control.ExportReady)
	if err != nil {
		return 0, err
	}
	n := 0
	now := a.Store.Now()
	for _, j := range jobs {
		if j.ExpiresAt.IsZero() || now.Before(j.ExpiresAt) {
			continue
		}
		if j.Path != "" {
			_ = os.Remove(j.Path)
		}
		if err := a.Store.ExpireExportJob(ctx, j.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

const exportReadme = `Shop export
===========

This archive holds everything in your Shop household.

  account.json     your own account (the person who asked for this export)
  household.json   the household and its members (display names and roles; other members'
                   email addresses and codes are never included)
  manifest.json    what is in this archive and when it was made
  data/            one JSON file per table of the household database (rows as objects;
                   binary values are base64). Money is integer cents.
  images/          receipt photos and product pictures, with the folder layout the server uses

Nothing in this archive is secret to you, but treat it like your receipts: it shows what you buy.
`

// buildExport writes the zip to ExportDir/<household>/<job>.zip (via a temporary file).
func (a *API) buildExport(ctx context.Context, j control.ExportJob) (string, int64, error) {
	if err := a.provision(ctx, j.HouseholdID); err != nil {
		return "", 0, err
	}
	t, release, err := a.Provider.Acquire(ctx, j.HouseholdID)
	if err != nil {
		return "", 0, err
	}
	defer release()
	dir := filepath.Join(a.Cfg.ExportDir, j.HouseholdID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	final := filepath.Join(dir, j.ID+".zip")
	tmp, err := os.CreateTemp(dir, j.ID+"-*.part")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name())
	zw := zip.NewWriter(tmp)
	var files []string
	addJSON := func(name string, v any) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		files = append(files, name)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	if w, err := zw.Create("README.txt"); err != nil {
		return "", 0, err
	} else if _, err := io.WriteString(w, exportReadme); err != nil {
		return "", 0, err
	}
	files = append(files, "README.txt")

	acc, err := a.Store.AccountByID(ctx, j.RequestedBy)
	if err != nil {
		return "", 0, err
	}
	if err := addJSON("account.json", map[string]any{"id": acc.ID, "display_name": sptr(acc.DisplayName), "email": sptr(acc.Email),
		"support_code": acc.SupportCode, "created_at": acc.CreatedAt}); err != nil {
		return "", 0, err
	}
	h, err := a.Store.HouseholdByID(ctx, j.HouseholdID)
	if err != nil {
		return "", 0, err
	}
	members, err := a.Store.Members(ctx, h.ID)
	if err != nil {
		return "", 0, err
	}
	ms := []map[string]any{}
	for _, m := range members {
		ms = append(ms, map[string]any{"display_name": sptr(m.DisplayName), "role": m.Role, "joined_at": m.JoinedAt, "is_you": m.AccountID == j.RequestedBy})
	}
	if err := addJSON("household.json", map[string]any{"id": h.ID, "name": h.Name, "created_at": h.CreatedAt, "you_are_owner": h.OwnerAccountID == j.RequestedBy, "members": ms}); err != nil {
		return "", 0, err
	}
	version, _ := store.MigrationVersion(ctx, t.DB)
	if err := a.dumpTables(ctx, t.DB, zw, &files); err != nil {
		return "", 0, err
	}
	if t.ImagesDir != "" {
		err := filepath.WalkDir(t.ImagesDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(t.ImagesDir, p)
			if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "logos/") { // the logo cache is not the user's data
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			src, err := os.Open(p)
			if err != nil {
				return nil // vanished while exporting
			}
			defer src.Close()
			name := "images/" + filepath.ToSlash(rel)
			w, err := zw.Create(name)
			if err != nil {
				return err
			}
			files = append(files, name)
			_, err = io.Copy(w, src)
			return err
		})
		if err != nil {
			return "", 0, err
		}
	}
	if err := addJSON("manifest.json", map[string]any{"format": "shop-export-2", "exported_at": a.Store.Now(), "schema_version": version, "files": files}); err != nil {
		return "", 0, err
	}
	if err := zw.Close(); err != nil {
		return "", 0, err
	}
	st, err := tmp.Stat()
	if err != nil {
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", 0, err
	}
	return final, st.Size(), nil
}

// skipTables never leave the server: tokens of the household database and its migration bookkeeping.
var skipTables = map[string]bool{"api_tokens": true, "jobs": true, "_schema_bump": true, "goose_db_version": true}

// dumpTables writes data/<table>.json for every table, in one read transaction.
func (a *API) dumpTables(ctx context.Context, db *sql.DB, zw *zip.Writer, files *[]string) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		if !skipTables[n] {
			tables = append(tables, n)
		}
	}
	rows.Close()
	for _, tb := range tables {
		name := "data/" + tb + ".json"
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		*files = append(*files, name)
		q := `SELECT * FROM "` + strings.ReplaceAll(tb, `"`, `""`) + `"`
		if tb == "settings" { // keep the pairing password hash out
			q += ` WHERE key NOT LIKE '%password%'`
		}
		if err := dumpOne(ctx, tx, q, w); err != nil {
			// A table without a key column (settings shape differs): retry without the filter only for settings.
			if tb != "settings" {
				return fmt.Errorf("table %s: %w", tb, err)
			}
			return fmt.Errorf("table %s: %w", tb, err)
		}
	}
	return nil
}

func dumpOne(ctx context.Context, tx *sql.Tx, q string, w io.Writer) error {
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	io.WriteString(w, "[")
	first := true
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		obj := make(map[string]any, len(cols))
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				obj[c] = base64.StdEncoding.EncodeToString(v)
			default:
				obj[c] = v
			}
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		if !first {
			io.WriteString(w, ",\n")
		} else {
			io.WriteString(w, "\n")
		}
		first = false
		w.Write(b)
	}
	io.WriteString(w, "\n]\n")
	return rows.Err()
}
