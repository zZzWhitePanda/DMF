// Package static embeds the compiled Next.js dashboard at build time.
//
// The Dockerfile replaces internal/static/dashboard-build/ with the
// real `next build` output before `go build` runs. Locally during Go
// development without a Next.js build, the placeholder index.html
// renders a "run npm run build" prompt.
package static

import (
	"embed"
	"io/fs"
)

//go:embed all:dashboard-build
var dashboardFS embed.FS

// Dashboard returns the embedded dashboard filesystem rooted at the
// dashboard-build directory. Pass to http.FileServer / fs.Stat.
func Dashboard() fs.FS {
	sub, err := fs.Sub(dashboardFS, "dashboard-build")
	if err != nil {
		// Shouldn't happen — the embed directive is static. If it
		// does, we fall back to the full FS which includes the
		// dashboard-build/ prefix (still serves content, just at
		// weird paths).
		return dashboardFS
	}
	return sub
}
