// Package version exposes the application version string.
//
// The default value is the in-source version. Release builds should override
// it at link time:
//
//	go build -ldflags "-X sharetext/internal/version.Version=v1.2.3" ./cmd/server
//
// A GitHub Actions workflow can pass the tag verbatim:
//
//	-ldflags "-X sharetext/internal/version.Version=${{ github.ref_name }}"
package version

// Version is the current application version. Overridable via -ldflags -X.
var Version = "v1.2.0"
