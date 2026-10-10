// Package receipts owns the receipt lifecycle: ingest, OCR, parse,
// persist, review edits, and re-processing.
package receipts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"shop/internal/jobs"
	"shop/internal/match"
	"shop/internal/ocr"
	"shop/internal/receipt"
	"shop/internal/store"
	"shop/internal/store/db"
)

const JobProcess = "process_receipt"

var (
	ErrNotFound      = errors.New("not found")
	ErrUnsupported   = errors.New("unsupported image type; use JPEG or PNG")
	ErrEdited        = errors.New("receipt was confirmed or edited; use force to discard edits")
	ErrInvalid       = errors.New("invalid input")
	errNoOCR         = errors.New("no stored OCR output")
	ErrHEIC          = fmt.Errorf("%w: HEIC/HEIF is not accepted; convert the photo to JPEG before uploading", ErrUnsupported)
	allowedImageType = map[string]string{"image/jpeg": ".jpg", "image/png": ".png"}
)

// DuplicateError is returned when the same image was uploaded before.
type DuplicateError struct{ ID int64 }

func (e DuplicateError) Error() string { return fmt.Sprintf("duplicate of receipt %d", e.ID) }

type Service struct {
	conn      *sql.DB
	q         *db.Queries
	ocr       *ocr.Client
	fallback  *ocr.Client
	queue     *jobs.Queue
	imagesDir string
	loc       *time.Location
	log       *slog.Logger
}

type Config struct {
	ImagesDir string
	Location  *time.Location
}

func New(conn *sql.DB, ocrClient, fallback *ocr.Client, queue *jobs.Queue, cfg Config, log *slog.Logger) *Service {
	s := &Service{
		conn: conn, q: db.New(conn), ocr: ocrClient, fallback: fallback, queue: queue,
		imagesDir: cfg.ImagesDir, loc: cfg.Location, log: log,
	}
	queue.Register(JobProcess, jobs.Handler{Run: s.runJob, OnFail: s.onJobFail})
	return s
}

type processPayload struct {
	ReceiptID int64 `json:"receipt_id"`
	OCR       bool  `json:"ocr"` // run OCR first (otherwise parse stored output)
}

// Ingest stores the image, creates a pending receipt and queues processing.
func (s *Service) Ingest(ctx context.Context, shopID int64, purchasedAt *time.Time, image []byte) (db.Receipt, error) {
	ext, ok := allowedImageType[http.DetectContentType(image)]
	if !ok {
		if isHEIF(image) {
			return db.Receipt{}, ErrHEIC
		}
		return db.Receipt{}, ErrUnsupported
	}
	if _, err := s.q.GetShop(ctx, shopID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.Receipt{}, fmt.Errorf("%w: shop %d does not exist", ErrInvalid, shopID)
		}
		return db.Receipt{}, err
	}

	sum := sha256.Sum256(image)
	sha := hex.EncodeToString(sum[:])
	if existing, err := s.q.GetReceiptBySHA(ctx, sha); err == nil {
		return existing, DuplicateError{ID: existing.ID}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return db.Receipt{}, err
	}

	rel := filepath.Join("receipts", sha[:2], sha+ext)
	if err := writeFileAtomic(filepath.Join(s.imagesDir, rel), image); err != nil {
		return db.Receipt{}, err
	}

	var rec db.Receipt
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := store.Now()
		var err error
		rec, err = q.CreateReceipt(ctx, db.CreateReceiptParams{
			ShopID: shopID, PurchasedAt: store.NullTS(purchasedAt), DateLocked: boolInt(purchasedAt != nil), ImagePath: rel,
			ImageSha256: sha, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		return jobs.Enqueue(ctx, q, JobProcess, processPayload{ReceiptID: rec.ID, OCR: true})
	})
	if err != nil {
		return db.Receipt{}, err
	}
	s.queue.Notify()
	return rec, nil
}

// Requeue schedules a re-parse (ocr=false) or re-OCR (ocr=true).
func (s *Service) Requeue(ctx context.Context, id int64, withOCR, force bool) (db.Receipt, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return rec, err
	}
	if !force {
		edited, err := s.isEdited(ctx, rec)
		if err != nil {
			return rec, err
		}
		if edited {
			return rec, ErrEdited
		}
	}
	if !withOCR && !rec.RawOcrJson.Valid {
		withOCR = true
	}
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		if force {
			if err := q.ClearHeaderEdited(ctx, db.ClearHeaderEditedParams{UpdatedAt: store.Now(), ID: id}); err != nil {
				return err
			}
		}
		if err := q.SetReceiptStatus(ctx, db.SetReceiptStatusParams{Status: "pending", UpdatedAt: store.Now(), ID: id}); err != nil {
			return err
		}
		return jobs.Enqueue(ctx, q, JobProcess, processPayload{ReceiptID: id, OCR: withOCR})
	})
	if err != nil {
		return rec, err
	}
	s.queue.Notify()
	return s.get(ctx, id)
}

func (s *Service) isEdited(ctx context.Context, rec db.Receipt) (bool, error) {
	if rec.ConfirmedAt.Valid || rec.HeaderEdited != 0 {
		return true, nil
	}
	lines, err := s.q.ListLines(ctx, rec.ID)
	if err != nil {
		return false, err
	}
	for _, l := range lines {
		if l.UserEdited != 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) runJob(ctx context.Context, payload []byte) error {
	var p processPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return jobs.Permanent(err)
	}
	return s.Process(ctx, p.ReceiptID, p.OCR)
}

func (s *Service) onJobFail(ctx context.Context, payload []byte, err error) {
	var p processPayload
	if json.Unmarshal(payload, &p) != nil {
		return
	}
	_ = s.q.SetReceiptStatus(ctx, db.SetReceiptStatusParams{Status: "failed", UpdatedAt: store.Now(), ID: p.ReceiptID})
}

// Process runs OCR (optionally) and the parser for one receipt, then
// replaces its lines.
func (s *Service) Process(ctx context.Context, id int64, withOCR bool) error {
	rec, err := s.q.GetReceipt(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.Permanent(fmt.Errorf("receipt %d: %w", id, ErrNotFound))
	}
	if err != nil {
		return err
	}
	if err := s.q.SetReceiptStatus(ctx, db.SetReceiptStatusParams{Status: "processing", UpdatedAt: store.Now(), ID: id}); err != nil {
		return err
	}

	var res ocr.Result
	if withOCR || !rec.RawOcrJson.Valid {
		if res, err = s.recognize(ctx, s.ocr, rec); err != nil {
			return err
		}
	} else if err := json.Unmarshal([]byte(rec.RawOcrJson.String), &res); err != nil {
		return jobs.Permanent(fmt.Errorf("%w: %v", errNoOCR, err))
	}

	parsed := receipt.Parse(res, receipt.Options{Location: s.loc, Now: time.Now()})

	// One retry with the fallback engine when the primary result needs review.
	if withOCR && s.fallback != nil && len(parsed.Reasons) > 0 {
		if alt, err := s.recognize(ctx, s.fallback, rec); err != nil {
			s.log.Warn("fallback ocr failed", "receipt", id, "err", err)
		} else if p2 := receipt.Parse(alt, receipt.Options{Location: s.loc, Now: time.Now()}); len(p2.Reasons) < len(parsed.Reasons) {
			res, parsed = alt, p2
		}
	}
	return s.persist(ctx, rec, res, parsed)
}

func (s *Service) recognize(ctx context.Context, c *ocr.Client, rec db.Receipt) (ocr.Result, error) {
	img, err := os.ReadFile(filepath.Join(s.imagesDir, rec.ImagePath))
	if err != nil {
		return ocr.Result{}, jobs.Permanent(err)
	}
	return c.Recognize(ctx, filepath.Base(rec.ImagePath), bytes.NewReader(img))
}

func (s *Service) persist(ctx context.Context, rec db.Receipt, res ocr.Result, parsed receipt.Result) error {
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	status := "ok"
	if len(parsed.Reasons) > 0 {
		status = "needs_review"
	}
	reasons, _ := json.Marshal(parsed.Reasons)

	return store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := store.Now()
		if err := q.SetReceiptOCR(ctx, db.SetReceiptOCRParams{
			OcrEngine: store.Str(res.Engine), OcrMs: sql.NullInt64{Int64: res.MS, Valid: true},
			RawOcrJson: store.Str(string(raw)), UpdatedAt: now, ID: rec.ID,
		}); err != nil {
			return err
		}
		if err := q.DeleteLinesForReceipt(ctx, rec.ID); err != nil {
			return err
		}
		ids := make([]int64, len(parsed.Lines))
		for i, l := range parsed.Lines {
			norm := match.Normalise(l.RawText)
			var parent sql.NullInt64
			if l.Parent >= 0 && l.Parent < i {
				parent = sql.NullInt64{Int64: ids[l.Parent], Valid: true}
			}
			var product sql.NullInt64
			if l.Kind == receipt.KindItem && norm != "" {
				if pid, err := q.LookupAlias(ctx, db.LookupAliasParams{NormText: norm, ShopID: sql.NullInt64{Int64: rec.ShopID, Valid: true}}); err == nil {
					product = sql.NullInt64{Int64: pid, Valid: true}
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			row, err := q.InsertLine(ctx, db.InsertLineParams{
				ReceiptID: rec.ID, LineNo: int64(i + 1), RawText: l.RawText, NormText: norm,
				Kind: string(l.Kind), Qty: l.Qty, Unit: l.Unit, UnitPriceCents: store.NullInt(l.UnitPriceCents),
				LineTotalCents: l.LineTotalCents, VatCode: store.Str(l.VATCode), ParentLineID: parent,
				ProductID: product,
			})
			if err != nil {
				return err
			}
			ids[i] = row.ID
		}
		return q.SetReceiptParsed(ctx, db.SetReceiptParsedParams{
			PurchasedAt:    store.NullTS(parsed.PurchasedAt), // ignored when date_locked
			TotalCents:     store.NullInt(parsed.TotalCents),
			ParsedSumCents: sql.NullInt64{Int64: parsed.SumCents, Valid: true},
			PaymentMethod:  store.Str(parsed.PaymentMethod),
			Status:         status,
			ReviewReasons:  store.Str(string(reasons)),
			ParserVersion:  sql.NullInt64{Int64: receipt.ParserVersion, Valid: true},
			UpdatedAt:      now,
			ID:             rec.ID,
		})
	})
}

func (s *Service) get(ctx context.Context, id int64) (db.Receipt, error) {
	rec, err := s.q.GetReceipt(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, ErrNotFound
	}
	return rec, err
}

// Delete removes the receipt, its lines, and its image file.
func (s *Service) Delete(ctx context.Context, id int64) error {
	rec, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.q.DeleteReceipt(ctx, id); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.imagesDir, rec.ImagePath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("remove receipt image", "path", rec.ImagePath, "err", err)
	}
	return nil
}

// ImagePath returns the absolute path of the receipt image.
func (s *Service) ImagePath(rec db.Receipt) string {
	return filepath.Join(s.imagesDir, rec.ImagePath)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil // content-addressed: already there
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// isHEIF reports an ISO-BMFF "ftyp" box with a HEIC/HEIF brand (iPhone photos).
func isHEIF(b []byte) bool {
	if len(b) < 12 || string(b[4:8]) != "ftyp" {
		return false
	}
	switch string(b[8:12]) {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1":
		return true
	}
	return false
}
