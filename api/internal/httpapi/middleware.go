package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"shop/internal/auth"
	"shop/internal/store"
	"shop/internal/store/db"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush keeps streaming responses (MCP over SSE) working through the recorder.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler { return logRequests(s.Log, next) }

func (s *Server) recoverer(next http.Handler) http.Handler { return recoverer(s.Log, next) }

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" && rec.status == http.StatusOK {
			return // docker healthchecks would flood the log
		}
		log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"ms", time.Since(start).Milliseconds())
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("panic", "path", r.URL.Path, "panic", v)
				writeProblem(w, http.StatusInternalServerError, "internal error", "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type tokenKey struct{}

// currentToken returns the API token that authenticated the request.
func currentToken(ctx context.Context) (db.ApiToken, bool) {
	t, ok := ctx.Value(tokenKey{}).(db.ApiToken)
	return t, ok
}

// ExternalAuth marks a request as already authenticated by the layer in front of the household's
// Server (the hosted gateway, which keeps device tokens in its control database, not in the
// household database). It can only be set by code that builds the request context, never by a
// client, so single mode (nothing sets it) is unchanged.
type ExternalAuth struct {
	DeviceID int64
	Name     string
	Kind     string
}

type externalAuthKey struct{}

// WithExternalAuth returns a context that makes the household Server skip its own token check.
func WithExternalAuth(ctx context.Context, a ExternalAuth) context.Context {
	return context.WithValue(ctx, externalAuthKey{}, a)
}

func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a, ok := r.Context().Value(externalAuthKey{}).(ExternalAuth); ok {
			kind := a.Kind
			if kind == "" {
				kind = "device"
			}
			tok := db.ApiToken{ID: a.DeviceID, Name: a.Name, Kind: kind}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, tok)))
			return
		}
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="shop"`)
			writeProblem(w, http.StatusUnauthorized, "missing bearer token", "")
			return
		}
		tok, err := auth.Verify(r.Context(), s.q, strings.TrimSpace(raw))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="shop", error="invalid_token"`)
			writeProblem(w, http.StatusUnauthorized, "invalid token", "")
			return
		}
		// Throttle last_used_at writes to once a minute per token.
		if last, err := store.ParseTS(tok.LastUsedAt.String); !tok.LastUsedAt.Valid || err != nil || time.Since(last) > time.Minute {
			_ = s.q.TouchToken(r.Context(), db.TouchTokenParams{LastUsedAt: store.Str(store.Now()), ID: tok.ID})
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, tok)))
	})
}
