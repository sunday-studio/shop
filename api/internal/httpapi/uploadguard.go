package httpapi

import (
	"context"
	"net/http"
)

// UploadGuard lets the layer in front of a household's Server (the hosted gateway) check and count a
// receipt upload once the real size of the photo is known. It returns false after answering the
// request itself (413, 429, ...). Otherwise finish(ok) is called when the upload is done; ok is
// false when ingesting failed, so the guard can give the quota back. Nothing sets a guard in single
// mode.
type UploadGuard func(w http.ResponseWriter, r *http.Request, size int64) (finish func(ok bool), proceed bool)

type uploadGuardKey struct{}

// WithUploadGuard returns a context carrying the guard for POST /v1/receipts.
func WithUploadGuard(ctx context.Context, g UploadGuard) context.Context {
	return context.WithValue(ctx, uploadGuardKey{}, g)
}

func uploadGuardFrom(ctx context.Context) UploadGuard {
	g, _ := ctx.Value(uploadGuardKey{}).(UploadGuard)
	return g
}
