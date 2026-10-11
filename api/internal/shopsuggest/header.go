package shopsuggest

import (
	"context"
	"database/sql"
	"encoding/json"

	"shop/internal/match"
	"shop/internal/store/db"
)

// DetectVersion is stored with every receipt header. Raise it when Detect changes so old headers are read
// again (Reindex does that in the background after a start).
const DetectVersion = 1

type storedHeader struct {
	Chain    string   `json:"chain"`
	Known    bool     `json:"known"`
	Branch   string   `json:"branch"`
	Key      string   `json:"key"`
	Street   string   `json:"street"`
	Number   string   `json:"number"`
	Town     string   `json:"town"`
	Evidence []string `json:"evidence"`
	Conf     float64  `json:"conf"`
	Postcode bool     `json:"postcode"`
}

func marshalHeader(d *Detected) (string, string, string) {
	if d == nil {
		return "null", "", ""
	}
	b, _ := json.Marshal(storedHeader{Chain: d.Chain, Known: d.Known, Branch: d.Branch, Key: d.Key, Street: d.Street,
		Number: d.Number, Town: d.town, Evidence: d.Evidence, Conf: d.Conf, Postcode: d.hasPostcode})
	return string(b), match.Fold(d.Chain), d.Number
}

func unmarshalHeader(js string) *Detected {
	if js == "" || js == "null" {
		return nil
	}
	var h storedHeader
	if json.Unmarshal([]byte(js), &h) != nil {
		return nil
	}
	return &Detected{Chain: h.Chain, Known: h.Known, Branch: h.Branch, Key: h.Key, Street: h.Street, Number: h.Number,
		Evidence: h.Evidence, Conf: h.Conf, town: h.Town, hasPostcode: h.Postcode}
}

// StoreHeader reads the header of a receipt's OCR text and saves it with the receipt. Call it whenever the
// OCR text changes (the receipt pipeline does, in the transaction that stores the OCR).
func StoreHeader(ctx context.Context, q *db.Queries, receiptID int64, rawOCR string) error {
	d := detectFromJSON(sql.NullString{String: rawOCR, Valid: rawOCR != ""})
	js, chain, number := marshalHeader(d)
	return q.SetReceiptHeader(ctx, db.SetReceiptHeaderParams{
		HeaderJson: sql.NullString{String: js, Valid: true}, HeaderChain: sql.NullString{String: chain, Valid: true},
		HeaderNumber: sql.NullString{String: number, Valid: true}, HeaderVersion: sql.NullInt64{Int64: DetectVersion, Valid: true}, ID: receiptID,
	})
}

// headerOf returns a receipt's stored header, computing and saving it first when it is missing or old.
func (s *Service) headerOf(ctx context.Context, rec db.Receipt) (*Detected, error) {
	if rec.HeaderVersion.Valid && rec.HeaderVersion.Int64 == DetectVersion && rec.HeaderJson.Valid {
		return unmarshalHeader(rec.HeaderJson.String), nil
	}
	if !rec.RawOcrJson.Valid {
		return nil, nil
	}
	if err := StoreHeader(ctx, s.q, rec.ID, rec.RawOcrJson.String); err != nil {
		return nil, err
	}
	return detectFromJSON(rec.RawOcrJson), nil
}

// Reindex stores the header of every receipt that has none (or an old one). Safe to run while the server
// answers requests: it works in small transactions.
func (s *Service) Reindex(ctx context.Context) (int, error) {
	n := 0
	for {
		rows, err := s.q.ListReceiptsWithoutHeader(ctx, db.ListReceiptsWithoutHeaderParams{HeaderVersion: sql.NullInt64{Int64: DetectVersion, Valid: true}, Limit: 100})
		if err != nil || len(rows) == 0 {
			return n, err
		}
		tx, err := s.conn.BeginTx(ctx, nil)
		if err != nil {
			return n, err
		}
		q := db.New(tx)
		for _, r := range rows {
			if err := StoreHeader(ctx, q, r.ID, r.RawOcrJson.String); err != nil {
				tx.Rollback() //nolint:errcheck
				return n, err
			}
		}
		if err := tx.Commit(); err != nil {
			return n, err
		}
		n += len(rows)
	}
}
