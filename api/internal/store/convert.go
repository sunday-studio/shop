package store

import (
	"database/sql"
	"strings"
	"time"
)

// TimeFormat is how every timestamp is stored: UTC, second precision, so
// lexical order equals chronological order in SQL comparisons.
const TimeFormat = "2006-01-02T15:04:05Z"

// TS formats t for storage.
func TS(t time.Time) string { return t.UTC().Format(TimeFormat) }

// Now returns the current time formatted for storage.
func Now() string { return TS(time.Now()) }

// ParseTS parses a stored timestamp.
func ParseTS(s string) (time.Time, error) { return time.Parse(TimeFormat, s) }

// NullTS converts an optional time to a nullable column value.
func NullTS(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: TS(*t), Valid: true}
}

// TimePtr converts a nullable timestamp column to *time.Time.
func TimePtr(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t, err := ParseTS(s.String)
	if err != nil {
		return nil
	}
	return &t
}

func NullStr(p *string) sql.NullString {
	if p == nil || *p == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func Str(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func StrPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func NullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func IntPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

// IsUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
func IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
