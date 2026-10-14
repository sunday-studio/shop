// Package version holds build metadata, set via -ldflags at build time.
package version

var (
	Version = "dev"
	Commit  = "none"
)

// APILevel is the revision of the API contract (see GET /v1/server-info). It rises whenever a field or
// route is added, so the app can tell which features a server has.
const APILevel = 8

// MinAppBuild is the oldest app build this server accepts.
const MinAppBuild = 1
