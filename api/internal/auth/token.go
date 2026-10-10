// Package auth issues and verifies API bearer tokens. Only SHA-256 hashes
// are stored; the plain token is shown once at creation.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"

	"shop/internal/store"
	"shop/internal/store/db"
)

const prefix = "st_"

var ErrInvalidToken = errors.New("invalid token")

func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func generate() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// Create stores a new named token and returns the plain token.
func Create(ctx context.Context, q *db.Queries, name string) (string, error) {
	tok := generate()
	_, err := q.CreateToken(ctx, db.CreateTokenParams{Name: name, TokenHash: Hash(tok), CreatedAt: store.Now()})
	return tok, err
}

// Verify returns the token row for a presented bearer token.
func Verify(ctx context.Context, q *db.Queries, token string) (db.ApiToken, error) {
	if !strings.HasPrefix(token, prefix) {
		return db.ApiToken{}, ErrInvalidToken
	}
	t, err := q.GetTokenByHash(ctx, Hash(token))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrInvalidToken
	}
	return t, err
}
