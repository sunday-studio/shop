package hostedapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func tptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func sptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// decodeJSON reads a small JSON body into v. An empty body is accepted when optional is true.
// Unknown fields are ignored (the contract says servers ignore them).
func decodeJSON(r *http.Request, v any, optional bool) error {
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return invalid("the request body could not be read")
	}
	if len(b) == 0 {
		if optional {
			return nil
		}
		return invalid("a JSON body is required")
	}
	if err := json.Unmarshal(b, v); err != nil {
		return invalid("the request body is not valid JSON")
	}
	return nil
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
